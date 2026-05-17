package expose

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/eric2788/bilirec/internal/modules/config"
	"github.com/eric2788/bilirec/utils"
	"github.com/fatedier/frp/client"
	"github.com/fatedier/frp/client/proxy"
	"github.com/fatedier/frp/pkg/config/source"
	v1 "github.com/fatedier/frp/pkg/config/v1"
	"github.com/sirupsen/logrus"
	"go.uber.org/fx"
	xwidth "golang.org/x/text/width"
)

const (
	defaultFRPServerPort    = 7000
	defaultLoopbackIP       = "127.0.0.1"
	officialFRPServer       = "tunnel.bilirec.org:7000"
	officialFRPDomain       = "tunnel.bilirec.org"
	tunnelStatusPollPeriod  = 1 * time.Second
	tunnelStatusWaitTimeout = 15 * time.Second
)

var logger = logrus.WithField("service", "expose")

type tunnelService interface {
	Run(context.Context) error
	StatusExporter() client.StatusExporter
}

type Service struct {
	svc tunnelService

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewService(lc fx.Lifecycle, cfg *config.Config) *Service {
	s := &Service{}
	if !cfg.FRPEnabled {
		return s
	}

	// Log FRP mode and token source for audit/debugging (without exposing token value)
	modeLabel := frpModeLabel(cfg)
	logger.Infof("FRP 已启用，模式：%s", modeLabel)

	svc, proxyName, remoteURL, err := initService(cfg)
	if err != nil {
		logger.Errorf("初始化 FRP 客户端失败：%v", err)
		return s
	} else {
		s.svc = svc
	}
	scheme := utils.Ternary(cfg.ServerCrt != "" && cfg.ServerKey != "", "https", "http")
	lc.Append(fx.StartStopHook(
		func() error {
			s.ctx, s.cancel = context.WithCancel(context.Background())
			s.wg.Add(1)
			go s.Run()
			go s.waitAndPrintTunnelBox(fmt.Sprintf("%s://%s:%s", scheme, defaultLoopbackIP, cfg.Port), proxyName, remoteURL)
			return nil
		},
		func() error {
			s.cancel()
			s.wg.Wait()
			return nil
		},
	))
	return s
}

func frpModeLabel(cfg *config.Config) string {
	if cfg.FRPServer == officialFRPServer && cfg.FRPBaseDomain == officialFRPDomain {
		return "official-public"
	}
	return "custom-selfhost"
}

func (s *Service) Run() {
	defer s.wg.Done()
	if err := s.svc.Run(s.ctx); err != nil {
		logger.Errorf("FRP 客户端错误：%v", err)
	}
}

func (s *Service) waitAndPrintTunnelBox(local, proxyName, remoteURL string) {
	ticker := time.NewTicker(tunnelStatusPollPeriod)
	defer ticker.Stop()

	timeout := time.NewTimer(tunnelStatusWaitTimeout)
	defer timeout.Stop()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-timeout.C:
			logger.Warnf("等待 FRP 代理 %q 变为运行状态超时；未打印隧道信息框", proxyName)
			return
		case <-ticker.C:
			if exporter := s.svc.StatusExporter(); exporter != nil {
				if status, ok := exporter.GetProxyStatus(proxyName); ok && status != nil {
					switch status.Phase {
					case proxy.ProxyPhaseRunning:
						printTunnelBox(local, remoteURL)
						return
					case proxy.ProxyPhaseStartErr:
						logger.Errorf("代理 %q 启动失败：%s", proxyName, status.Err)
						return
					case proxy.ProxyPhaseCheckFailed:
						logger.Errorf("代理 %q 检查失败：%s", proxyName, status.Err)
						return
					}
				}
			}
		}
	}
}

func initService(cfg *config.Config) (*client.Service, string, string, error) {
	addr, port, err := parseServerAddr(cfg.FRPServer)
	if err != nil {
		return nil, "", "", fmt.Errorf("无效的 frp 服务器地址：%w", err)
	}
	configSource := source.NewConfigSource()
	random, err := utils.RandomHexString(12)
	if err != nil {
		return nil, "", "", fmt.Errorf("生成随机字符串失败：%w", err)
	}
	proxyName := fmt.Sprintf("bilirec-%s", random)
	if err := configSource.ReplaceAll([]v1.ProxyConfigurer{
		utils.TernaryFunc(
			cfg.FRPHttps,
			func() v1.ProxyConfigurer {
				return &v1.HTTPSProxyConfig{
					ProxyBaseConfig: v1.ProxyBaseConfig{
						Name: proxyName,
						Type: string(v1.ProxyTypeHTTPS),
						ProxyBackend: v1.ProxyBackend{
							LocalIP:   defaultLoopbackIP,
							LocalPort: utils.MustAtoi(cfg.Port),
						},
					},
					DomainConfig: v1.DomainConfig{
						SubDomain: random,
					},
				}
			},
			func() v1.ProxyConfigurer {
				return &v1.HTTPProxyConfig{
					ProxyBaseConfig: v1.ProxyBaseConfig{
						Name: proxyName,
						Type: string(v1.ProxyTypeHTTP),
						ProxyBackend: v1.ProxyBackend{
							LocalIP:   defaultLoopbackIP,
							LocalPort: utils.MustAtoi(cfg.Port),
						},
					},
					DomainConfig: v1.DomainConfig{
						SubDomain: random,
					},
				}
			},
		),
	}, nil); err != nil {
		return nil, "", "", err
	}
	remoteURL := fmt.Sprintf("%s%s.%s", utils.Ternary(cfg.FRPSchemeHttps, "https://", "http://"), random, cfg.FRPBaseDomain)
	ctx, err := client.NewService(client.ServiceOptions{
		Common: &v1.ClientCommonConfig{
			ServerAddr: addr,
			ServerPort: port,
			Auth: v1.AuthClientConfig{
				Method: v1.AuthMethodToken,
				Token:  cfg.FRPToken,
			},
		},
		ConfigSourceAggregator: source.NewAggregator(configSource),
	})
	return ctx, proxyName, remoteURL, err
}

func parseServerAddr(addr string) (string, int, error) {
	if addr == "" {
		return "", 0, fmt.Errorf("FRP_SERVER 为空")
	}

	host, portStr, err := net.SplitHostPort(addr)
	if err == nil {
		if host == "" {
			return "", 0, fmt.Errorf("无效的 FRP_SERVER 格式：%q", addr)
		}
		port, convErr := strconv.Atoi(portStr)
		if convErr != nil {
			return "", 0, fmt.Errorf("无效的 frp 服务器端口 %q：%w", portStr, convErr)
		}
		return host, port, nil
	}

	if shouldFallbackToDefaultPort(addr, err) {
		return addr, defaultFRPServerPort, nil
	}

	return "", 0, fmt.Errorf("无效的 FRP_SERVER 格式：%q: %w", addr, err)
}

func shouldFallbackToDefaultPort(addr string, err error) bool {
	if strings.Contains(addr, "://") || strings.Contains(addr, "[") || strings.Contains(addr, "]") || strings.Contains(addr, "%") {
		return false
	}
	if strings.Count(addr, ":") == 0 {
		return true
	}
	// Bare IP literals (including unbracketed IPv6 like ::1) are treated as host-only
	// and fall back to the default FRP server port.
	if ip := net.ParseIP(addr); ip != nil {
		return true
	}

	var addrErr *net.AddrError
	if errors.As(err, &addrErr) {
		return strings.Contains(addrErr.Err, "missing port in address")
	}

	return strings.Contains(err.Error(), "missing port in address")
}

func printTunnelBox(local, remote string) {
	const minWidth = 55
	const leftPadding = "  "
	title := "内网穿透已建立!"
	localLine := "本地地址:  " + local
	remoteLine := "远程地址:  " + remote

	// Width tracks the inner box width by terminal display width, not byte
	// length, so CJK text aligns with ASCII URLs in monospaced terminals.
	width := minWidth
	if l := displayWidth(leftPadding+title) + 1; l > width {
		width = l
	}
	if l := displayWidth(leftPadding+localLine) + 1; l > width {
		width = l
	}
	if l := displayWidth(leftPadding+remoteLine) + 1; l > width {
		width = l
	}

	edge := "+" + strings.Repeat("-", width) + "+"
	emptyLine := "|" + strings.Repeat(" ", width) + "|"

	fmt.Println()
	fmt.Println(edge)

	printBoxLine(width, title)

	fmt.Println(emptyLine)

	printBoxLine(width, localLine)
	printBoxLine(width, remoteLine)

	fmt.Println(emptyLine)

	fmt.Println(edge)
	fmt.Println()
}

func printBoxLine(width int, content string) {
	const leftPadding = "  "
	inner := leftPadding + content
	padding := width - displayWidth(inner)
	if padding < 0 {
		padding = 0
	}
	fmt.Printf("|%s%s|\n", inner, strings.Repeat(" ", padding))
}

func displayWidth(s string) int {
	total := 0
	for _, r := range s {
		if unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) || r == '\u200d' {
			continue
		}

		switch xwidth.LookupRune(r).Kind() {
		case xwidth.EastAsianWide, xwidth.EastAsianFullwidth:
			total += 2
		default:
			total += 1
		}
	}
	return total
}
