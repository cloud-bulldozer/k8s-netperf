package logging

import (
	"os"

	"github.com/sirupsen/logrus"
)

var defaultLog *logrus.Logger

func new() *(logrus.Logger) {
	var log = logrus.New()
	log.SetOutput(os.Stdout)
	log.SetLevel(logrus.InfoLevel)
	log.SetFormatter(&logrus.TextFormatter{
		DisableColors:   false,
		TimestampFormat: "2006-01-02 15:04:05",
		FullTimestamp:   true,
	})
	return log
}

// init - instance of logrus with our desired format
func init() {
	defaultLog = new()
}

// SetDebug - Switch to DEBUG level
func SetDebug() {
	SetLevel(defaultLog, logrus.DebugLevel)
}

// SetLevel - Provided logger, set the log level
func SetLevel(logger *logrus.Logger, level logrus.Level) {
	logger.SetLevel(level)
}

// Debug - Debug message
func Debug(args ...interface{}) {
	defaultLog.Debug(args...)
}

// Debugf - Debug message
func Debugf(format string, args ...interface{}) {
	defaultLog.Debugf(format, args...)
}

// Error - Error message
func Error(args ...interface{}) {
	defaultLog.Error(args...)
}

// Errorf - Error message
func Errorf(format string, args ...interface{}) {
	defaultLog.Errorf(format, args...)
}

// Info - Info Message
func Info(args ...interface{}) {
	defaultLog.Info(args...)
}

// Infof - Info Message
func Infof(format string, args ...interface{}) {
	defaultLog.Infof(format, args...)
}

// Warn - Warn Message
func Warn(args ...interface{}) {
	defaultLog.Warn(args...)
}

// Warnf - Warn Message
func Warnf(format string, args ...interface{}) {
	defaultLog.Warnf(format, args...)
}
