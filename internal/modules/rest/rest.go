// @title BiliRec API
// @version 1.0
// @description Bilibili Live Recording Service API
// @termsOfService http://swagger.io/terms/

// @contact.name API Support
// @contact.url http://github.com/eric2788/bilirec

// @license.name MIT
// @license.url https://opensource.org/licenses/MIT

// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization

// @BasePath /
//
//go:generate swag init -g rest.go -o ../../docs
package rest

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/eric2788/bilirec/docs"
	"github.com/eric2788/bilirec/internal/modules/config"
	"github.com/eric2788/bilirec/utils"

	"github.com/sirupsen/logrus"
	"go.uber.org/fx"

	jwt "github.com/gofiber/contrib/v3/jwt"
	"github.com/gofiber/contrib/v3/swagger"
	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/extractors"
	"github.com/gofiber/fiber/v3/middleware/cors"
	"github.com/gofiber/fiber/v3/middleware/limiter"
	logging "github.com/gofiber/fiber/v3/middleware/logger"
	"github.com/gofiber/fiber/v3/middleware/pprof"
	"github.com/gofiber/fiber/v3/middleware/recover"
)

const jwtTokenKey = "jwtToken"

const (
	mediaStreamCacheControl = "public, max-age=86400"
	defaultNoCacheControl   = "no-store, no-cache, must-revalidate"
)

var logger = logrus.WithField("module", "rest")

func provider(ls fx.Lifecycle, cfg *config.Config) *fiber.App {
	app := fiber.New(fiber.Config{
		TrustProxy:  true,
		ProxyHeader: fiber.HeaderXForwardedFor,
		TrustProxyConfig: fiber.TrustProxyConfig{
			Private:  true,
			Loopback: true,
			Proxies:  cfg.TrustedProxies,
		},
	})

	app.Use(recover.New())

	if cfg.Debug {
		hexStr := utils.RandomHexStringMust(32)
		logger.Infof("你可以使用十六进制令牌 %q 登录 /debug/pprof", hexStr)
		authenticate := func(c fiber.Ctx) error {
			if c.Get("Authorization") != hexStr {
				return fiber.ErrUnauthorized
			}
			return c.Next()
		}
		app.Use("/debug/pprof", authenticate, pprof.New())
	}

	app.Use(logging.New(logging.Config{
		TimeZone: utils.EmptyOrElse(os.Getenv("TZ"), "Asia/Hong_Kong"),
		Format:   "| ${status} | ${latency} | ${ip} | ${method} | ${path} | ${error}\n",
		Stream:   logger.Writer(),
		Next: func(c fiber.Ctx) bool {
			if cfg.SilentAccessLog {
				return c.Response().StatusCode() < 400
			}
			return false
		},
	}))

	if swaggerPath, ok := resolveSwaggerFilePath(); ok {
		app.Use(swagger.New(swagger.Config{
			BasePath: "/",
			FilePath: swaggerPath,
			Path:     "/",
			Title:    "BiliRec API Documentation",
		}))
	} else {
		logger.Warn("swagger 文件未找到，禁用 swagger 路由")
	}

	app.Use(cors.New(cors.Config{
		AllowOrigins: utils.Ternary(
			cfg.ProductionMode,
			[]string{cfg.FrontendURL.String()},
			[]string{
				cfg.FrontendURL.String(),
				"http://localhost:3000",
				"http://127.0.0.1:3000",
			},
		),
		AllowCredentials: true,
	}))

	app.Use(func(c fiber.Ctx) error {
		err := c.Next()

		contentType := strings.ToLower(c.GetRespHeader(fiber.HeaderContentType))
		if isMediaStreamContentType(contentType) {
			c.Set(fiber.HeaderCacheControl, mediaStreamCacheControl)
			return err
		}

		c.Set(fiber.HeaderCacheControl, defaultNoCacheControl)
		c.Set(fiber.HeaderPragma, "no-cache")
		c.Set(fiber.HeaderExpires, "0")
		return err
	})

	if cfg.Username != "" && cfg.PasswordHash != "" {
		logger.Info("REST API 已启用 JWT 认证")
		app.Post("/login",
			limiter.New(limiter.Config{Max: 10, Expiration: 1 * time.Minute}),
			loginHandler(cfg),
		)
		app.Post("/logout", logoutHandler(cfg))
		app.Use(jwt.New(jwt.Config{
			Next: func(c fiber.Ctx) bool {
				// paths that don't require JWT authentication
				exemptPaths := []string{
					"/files/tempdownload",
				}
				// allow CORS preflight requests
				if c.Method() == "OPTIONS" {
					return true
				}
				path := c.Path()
				for _, p := range exemptPaths {
					if strings.HasPrefix(path, p) {
						return true
					}
				}
				return false
			},
			Extractor:  extractors.FromCookie(jwtTokenKey),
			SigningKey: jwt.SigningKey{Key: []byte(cfg.JwtSecret)},
			ErrorHandler: func(c fiber.Ctx, err error) error {
				if errors.Is(err, extractors.ErrNotFound) {
					return c.Status(fiber.StatusUnauthorized).SendString(jwt.ErrMissingToken.Error())
				}
				if e, ok := err.(*fiber.Error); ok {
					return c.Status(e.Code).SendString(e.Message)
				}
				return c.Status(fiber.StatusUnauthorized).SendString("JWT 无效或已过期")
			},
		}))
	}

	ls.Append(
		fx.StartStopHook(
			func(ctx context.Context) error {
				addr := net.JoinHostPort(cfg.Host, cfg.Port)
				// Check if both SERVER_CRT and SERVER_KEY are provided
				if cfg.ServerCrt != "" && cfg.ServerKey != "" {
					return startHttpsServer(app, addr, cfg.ServerCrt, cfg.ServerKey)
				} else {
					return startHttpServer(app, addr)
				}
			},
			func(ctx context.Context) error {
				logger.Info("正在停止服务器")
				return app.ShutdownWithContext(ctx)
			},
		),
	)

	return app
}

func startHttpsServer(app *fiber.App, addr, serverCrt, serverKey string) error {
	logger.Infof("正在 %s 启动 HTTPS 服务器", addr)
	cert, err := tls.LoadX509KeyPair(serverCrt, serverKey)
	if err != nil {
		return fmt.Errorf("加载证书失败：%w", err)
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
	}

	listener, err := tls.Listen("tcp", addr, tlsConfig)
	if err != nil {
		return fmt.Errorf("HTTPS 服务器错误：%w", err)
	}
	go func() {
		if err := app.Listener(listener); err != nil {
			logger.Errorf("HTTPS 服务器错误：%v", err)
		}
	}()
	return nil
}

func startHttpServer(app *fiber.App, addr string) error {
	logger.Infof("正在 %s 启动 HTTP 服务器", addr)
	go func() {
		if err := app.Listen(addr); err != nil {
			logger.Errorf("HTTP 服务器错误：%v", err)
		}
	}()
	return nil
}

func isMediaStreamContentType(contentType string) bool {
	return strings.HasPrefix(contentType, "video/") ||
		strings.HasPrefix(contentType, "audio/") ||
		strings.HasPrefix(contentType, "application/vnd.apple.mpegurl") ||
		strings.HasPrefix(contentType, "application/x-mpegurl") ||
		strings.HasPrefix(contentType, "application/dash+xml")
}

func resolveSwaggerFilePath() (string, bool) {
	const relativeSwaggerPath = "./docs/swagger.json"

	if _, err := os.Stat(relativeSwaggerPath); err == nil {
		return relativeSwaggerPath, true
	}

	exe, err := os.Executable()
	if err != nil {
		return "", false
	}

	exeSwaggerPath := filepath.Join(filepath.Dir(exe), "docs", "swagger.json")
	if _, err := os.Stat(exeSwaggerPath); err != nil {
		return "", false
	}

	return exeSwaggerPath, true
}

var Module = fx.Module("rest", fx.Provide(provider))
