package log

import (
	"os"

	"github.com/fatih/color"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	ctrlruntimezap "sigs.k8s.io/controller-runtime/pkg/log/zap"
)

func New(debug bool) *zap.Logger {
	Use()

	zap.NewDevelopmentConfig()
	encCfg := zap.NewProductionEncoderConfig()
	encCfg.TimeKey = "timestamp"
	encCfg.EncodeTime = zapcore.ISO8601TimeEncoder

	lvl := zap.NewAtomicLevelAt(zap.InfoLevel)
	if debug {
		lvl = zap.NewAtomicLevelAt(zapcore.Level(-2))
	}

	encoder := zapcore.NewJSONEncoder(encCfg)
	core := zapcore.NewCore(&ctrlruntimezap.KubeAwareEncoder{Encoder: encoder}, zapcore.AddSync(os.Stderr), lvl)

	_ = zap.New(core)
	return NewDevZapLogger(zap.NewAtomicLevelAt(TraceLevel))
}

const TraceLevel = zapcore.DebugLevel - 1

func NewDevZapLogger(lvl zapcore.LevelEnabler) *zap.Logger {
	encCfg := zap.NewDevelopmentEncoderConfig()
	encCfg.EncodeLevel = capitalColorLevelEncoder
	zapCore := zapcore.NewCore(zapcore.NewConsoleEncoder(encCfg), zapcore.AddSync(os.Stderr), lvl)
	return zap.New(zapCore)
}

func capitalColorLevelEncoder(level zapcore.Level, enc zapcore.PrimitiveArrayEncoder) {
	if level == TraceLevel {
		enc.AppendString(color.CyanString("TRACE"))
		return
	}
	zapcore.CapitalColorLevelEncoder(level, enc)
}

func Use() {
	zap.ReplaceGlobals(NewDevZapLogger(zap.NewAtomicLevelAt(TraceLevel)))
}
