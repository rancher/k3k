package log

import (
	"os"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	ctrlruntimezap "sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func New(debug bool) *zap.Logger {
	encCfg := zap.NewProductionEncoderConfig()
	encCfg.TimeKey = "timestamp"
	encCfg.EncodeTime = zapcore.ISO8601TimeEncoder

	lvl := zap.NewAtomicLevelAt(zap.InfoLevel)
	if debug {
		lvl = zap.NewAtomicLevelAt(zap.DebugLevel)
	}

	encoder := zapcore.NewJSONEncoder(encCfg)
	core := zapcore.NewCore(&ctrlruntimezap.KubeAwareEncoder{Encoder: encoder}, zapcore.AddSync(os.Stderr), lvl)

	return zap.New(core)
}
