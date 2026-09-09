// Package logging builds the zap logger the k3k binaries write through.
package logging

import (
	"os"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	ctrlruntimezap "sigs.k8s.io/controller-runtime/pkg/log/zap"
)

// New returns a zap logger writing to stderr in the given format, at debug level when
// debug is set and info level otherwise.
func New(debug bool, format string) *zap.Logger {
	lvl := zap.NewAtomicLevelAt(zap.InfoLevel)
	if debug {
		lvl = zap.NewAtomicLevelAt(zap.DebugLevel)
	}

	encoder := newEncoder(format)
	core := zapcore.NewCore(encoder, zapcore.AddSync(os.Stderr), lvl)

	return zap.New(core)
}

func newEncoder(format string) zapcore.Encoder {
	encCfg := zap.NewProductionEncoderConfig()
	encCfg.TimeKey = "timestamp"
	encCfg.EncodeTime = zapcore.ISO8601TimeEncoder

	var encoder zapcore.Encoder

	if format == "text" {
		encCfg.EncodeLevel = zapcore.CapitalColorLevelEncoder
		encoder = zapcore.NewConsoleEncoder(encCfg)
	} else {
		encoder = zapcore.NewJSONEncoder(encCfg)
	}

	return &ctrlruntimezap.KubeAwareEncoder{Encoder: encoder}
}
