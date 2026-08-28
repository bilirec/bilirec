package utils

import (
	"strconv"
	"time"

	"github.com/bilirec/bilirec/pkg/logger"
)

func NilOrElse[T any](ptr *T, defaultValue T) T {
	if ptr == nil {
		return defaultValue
	}
	return *ptr
}

func Ptr[T any](v T) *T {
	return &v
}

func EmptyOrElse(s string, defaultValue string) string {
	if s == "" {
		return defaultValue
	}
	return s
}

func Ternary[T any](condition bool, trueValue, falseValue T) T {
	if condition {
		return trueValue
	} else {
		return falseValue
	}
}

func TernaryFunc[T any](condition bool, trueFunc, falseFunc func() T) T {
	if condition {
		return trueFunc()
	} else {
		return falseFunc()
	}
}

func MustAtoi(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		panic(err)
	}
	return n
}

func MustAtoi64(s string) int64 {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		panic(err)
	}
	return n
}

func WithRetry(attempts int, log logger.Logger, action string, fn func() error) error {
	var err error
	for i := range attempts {
		err = fn()
		if err == nil {
			log.Debugf("%s succeeded on attempt %d", action, i+1)
			return nil
		} else {
			log.Warnf("%s 在第 %d 次尝试时失败：%v", action, i+1, err)
		}
		if i < attempts-1 {
			sleep := time.Duration(1<<i) * time.Second
			log.Warnf("将在 %.f 秒后重试...", sleep.Seconds())
			time.Sleep(sleep)
		}
	}
	return err
}
