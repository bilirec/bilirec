package rw_test

import (
	"bytes"
	"os"
	"testing"

	"github.com/bilirec/bilirec/pkg/rw"
	"github.com/sirupsen/logrus"
)

func TestLogrusLogback(t *testing.T) {
	multiLine := "ffmpeg version n4.4.4 Copyright (c) 2000-2022 the FFmpeg developers\n" +
		"  built with gcc 11 (GCC) 20220124\n" +
		"  configuration: --enable-gpl --enable-libx264\n" +
		"  libavutil      56. 70.100 / 56. 70.100\n" +
		"  libavcodec     58.134.100 / 58.134.100\n" +
		"  libavformat    58. 76.100 / 58. 76.100\n" +
		"  libavfilter     7.110.100 / 7.110.100\n" +
		"  libswscale      5. 9.100 / 5. 9.100\n" +
		"  libswresample   3. 9.100 / 3. 9.100\n"

	f, err := os.CreateTemp(t.TempDir(), "logrus-*.log")
	if err != nil {
		t.Fatalf("failed to create temp log file: %v", err)
	}
	defer os.Remove(f.Name())

	origOut := logrus.StandardLogger().Out
	origFmt := logrus.StandardLogger().Formatter

	logrus.SetOutput(f)
	logrus.SetFormatter(&rw.MultiLineFormatter{TextFormatter: logrus.TextFormatter{
		TimestampFormat: "2006-01-02 15:04:05",
		FullTimestamp:   true,
		ForceColors:     false, // disable colors for file output
	}})

	logrus.Info(multiLine)

	// Restore immediately so other tests/loggers aren't affected (and avoid writing to a closed file).
	logrus.SetOutput(origOut)
	logrus.SetFormatter(origFmt)
	_ = f.Close()

	data, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatalf("failed to read log file: %v", err)
	}
	if !bytes.Contains(data, []byte("\n  built with gcc")) || bytes.Contains(data, []byte(`\\n  built with gcc`)) {
		t.Fatalf("unexpected formatter output: %q", string(data))
	}

}
