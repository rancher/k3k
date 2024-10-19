package log

import (
	"os"

	"github.com/virtual-kubelet/virtual-kubelet/log"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	ctrlruntimezap "sigs.k8s.io/controller-runtime/pkg/log/zap"
)

type Logger struct {
	*zap.SugaredLogger
}

func New(debug bool) *Logger {
	return &Logger{newZappLogger(debug).Sugar()}
}

func (k *Logger) WithError(err error) log.Logger {
	return k
}

func (k *Logger) WithField(string, interface{}) log.Logger {
	return k
}

func (k *Logger) WithFields(field log.Fields) log.Logger {
	return k
}

func (k *Logger) Named(name string) *Logger {
	k.SugaredLogger = k.SugaredLogger.Named(name)
	return k
}

func newZappLogger(debug bool) *zap.Logger {
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
