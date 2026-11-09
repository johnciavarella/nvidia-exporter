package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/woo/nvidia-exporter/internal/collector"
	"github.com/woo/nvidia-exporter/internal/intel"
	"github.com/woo/nvidia-exporter/internal/nvidia"
)

func main() {
	var (
		listenAddress   = flag.String("listen-address", ":9835", "Address to listen on for HTTP requests")
		metricsPath     = flag.String("metrics-path", "/metrics", "Path under which to expose metrics")
		backend         = flag.String("backend", "nvidia", "GPU backend to collect from: nvidia (nvidia-smi) or intel (intel_gpu_top)")
		nvidiaSMIPath   = flag.String("nvidia-smi-path", "nvidia-smi", "Path to nvidia-smi binary")
		intelGPUTopPath = flag.String("intel-gpu-top-path", "intel_gpu_top", "Path to intel_gpu_top binary (backend=intel)")
		intelDevice     = flag.String("intel-device", "", "intel_gpu_top device filter, e.g. drm:/dev/dri/renderD128 (backend=intel; default: first GPU)")
		intelHwmon      = flag.String("intel-hwmon", "auto", "Intel GPU hwmon directory for power/energy/temperature/fan (backend=intel): auto, off, or a path like /sys/class/drm/card1/device/hwmon/hwmon5")
		collectInterval = flag.Duration("collect-interval", 10*time.Second, "Interval between metric collections (sampling period for backend=intel)")
		logLevel        = flag.String("log-level", "info", "Log level (debug, info, warn, error)")
	)
	flag.Parse()

	// Setup logger
	level := parseLogLevel(*logLevel)
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)

	logger.Info("starting nvidia-exporter",
		"backend", *backend,
		"listen_address", *listenAddress,
		"metrics_path", *metricsPath,
		"nvidia_smi_path", *nvidiaSMIPath,
		"intel_gpu_top_path", *intelGPUTopPath,
		"collect_interval", *collectInterval,
		"log_level", *logLevel,
	)

	// Create context that cancels on SIGINT/SIGTERM
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Create the collector for the selected backend and start it
	reg := prometheus.NewRegistry()
	title := "NVIDIA Exporter"
	switch *backend {
	case "nvidia":
		provider := nvidia.NewSMIProvider(*nvidiaSMIPath)
		coll := collector.New(provider, *collectInterval, logger)
		reg.MustRegister(coll)
		go coll.Run(ctx)
	case "intel":
		title = "Intel GPU Exporter"
		device := *intelDevice
		if device == "" {
			device = "default"
		}
		provider := intel.NewGPUTopProvider(*intelGPUTopPath, *intelDevice, *collectInterval, logger)
		// A sample is stale after three sampling periods plus a startup margin.
		coll := collector.NewIntel(provider, device, 3**collectInterval+5*time.Second)
		reg.MustRegister(coll)
		if hw := openHwmon(*intelHwmon, *intelDevice, logger); hw != nil {
			coll.WithHwmon(hw)
		}
		go provider.Run(ctx)
	default:
		logger.Error("unknown backend, want nvidia or intel", "backend", *backend)
		os.Exit(2)
	}

	// Create HTTP server
	mux := http.NewServeMux()

	// Landing page
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head><title>%s</title></head>
<body>
<h1>%s</h1>
<p><a href="%s">Metrics</a></p>
</body>
</html>
`, title, title, *metricsPath)
	})

	// Metrics endpoint
	mux.Handle(*metricsPath, promhttp.HandlerFor(reg, promhttp.HandlerOpts{
		EnableOpenMetrics: true,
	}))

	// Health endpoint
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "OK")
	})

	server := &http.Server{
		Addr:    *listenAddress,
		Handler: mux,
	}

	// Handle signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Start HTTP server goroutine
	go func() {
		logger.Info("starting HTTP server", "address", *listenAddress)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("HTTP server error", "error", err)
			cancel()
		}
	}()

	// Wait for signal
	sig := <-sigCh
	logger.Info("received signal, shutting down", "signal", sig)

	// Cancel collector context
	cancel()

	// Graceful shutdown of HTTP server
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("HTTP server shutdown error", "error", err)
	}

	logger.Info("shutdown complete")
}

// openHwmon resolves the --intel-hwmon flag. It returns nil (hwmon metrics
// disabled) for "off", or for "auto" when no Intel GPU hwmon device is found.
func openHwmon(setting, device string, logger *slog.Logger) *intel.Hwmon {
	dir := setting
	switch setting {
	case "off":
		return nil
	case "auto":
		var err error
		dir, err = intel.FindHwmon("/sys", intel.PCISlotFromDevice(device))
		if err != nil {
			logger.Info("no Intel GPU hwmon found, hwmon metrics disabled", "error", err)
			return nil
		}
	}
	hw := intel.NewHwmon(dir)
	if _, err := hw.Read(); err != nil {
		logger.Warn("hwmon not readable, hwmon metrics disabled", "dir", dir, "error", err)
		return nil
	}
	logger.Info("hwmon metrics enabled", "dir", dir)
	return hw
}

func parseLogLevel(level string) slog.Level {
	switch level {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
