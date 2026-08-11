package metrics

import (
	"context"
	"errors"
	"net"
	"net/http"

	"github.com/bilirec/bilirec/internal/modules/config"
	"go.uber.org/fx"
)

// registerServer 在獨立 port 暴露 /metrics，不掛進主 API server，完全不影響原有路由與認證。
func (e *Exporter) registerServer(lc fx.Lifecycle, cfg *config.Config) {
	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, _ *http.Request) {
		e.set.WritePrometheus(w)
	})
	srv := &http.Server{Handler: mux}

	lc.Append(fx.StartStopHook(
		func(context.Context) error {
			addr := net.JoinHostPort(cfg.MetricsHost, cfg.MetricsPort)
			ln, err := net.Listen("tcp", addr)
			if err != nil {
				return err
			}
			go func() {
				if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
					logger.Errorf("metrics 服务器错误：%v", err)
				}
			}()
			logger.Infof("metrics 服务器已启动：http://%s/metrics", addr)
			return nil
		},
		func(ctx context.Context) error {
			return srv.Shutdown(ctx)
		},
	))
}
