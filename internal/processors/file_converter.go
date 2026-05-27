package processors

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/bilirec/bilirec/pkg/pipeline"
	"github.com/bilirec/bilirec/utils"
	"github.com/sirupsen/logrus"
)

type FileConverterProcessor struct {
	deleteSource bool
	oldPath      string
	destPath     string
	format       string

	initTimeout time.Duration
}

type FileConverterOption func(*FileConverterProcessor)

func NewFileConverter(format string, options ...FileConverterOption) *pipeline.ProcessorInfo[string] {
	fc := &FileConverterProcessor{
		format:      "." + format,
		initTimeout: 5 * time.Minute,
	}
	for _, option := range options {
		option(fc)
	}
	return pipeline.NewProcessorInfo(
		fmt.Sprintf("%s-file-converter", format),
		fc,
		pipeline.WithTimeout[string](fc.initTimeout),
	)
}

func (p *FileConverterProcessor) Open(ctx context.Context, log *logrus.Entry) error {
	cmd := exec.CommandContext(ctx, "ffmpeg", "-h")
	if err := cmd.Run(); err != nil {
		return err
	}
	if p.destPath != "" && !strings.HasSuffix(p.destPath, p.format) {
		return fmt.Errorf("destPath %s 未以 %s 结尾", p.destPath, p.format)
	}
	return nil
}

func (p *FileConverterProcessor) Process(ctx context.Context, log *logrus.Entry, path string) (string, error) {
	if strings.HasSuffix(path, p.format) {
		log.Debugf("file %s already in %s format, skipping conversion", path, p.format)
		return path, nil
	}
	p.oldPath = path
	newFileName := utils.EmptyOrElse(p.destPath, path[0:strings.LastIndex(path, ".")]+p.format)
	convertCmd := exec.CommandContext(ctx,
		"ffmpeg",
		"-hide_banner",
		"-i",
		path,
		"-map",
		"0:v",
		"-map",
		"0:a?",
		"-movflags",
		"+faststart",
		"-c",
		"copy",
		newFileName,
	)
	convertCmd.Stderr = log.Writer()
	convertCmd.Stdout = log.Writer()
	if err := convertCmd.Run(); err != nil {
		return path, err
	}
	return newFileName, nil
}

func (p *FileConverterProcessor) Close() error {
	if p.deleteSource && p.oldPath != "" {
		return os.Remove(p.oldPath)
	}
	return nil
}

func NewMp4FileConverter(options ...FileConverterOption) *pipeline.ProcessorInfo[string] {
	return NewFileConverter("mp4", options...)
}

func NewAviFileConverter(options ...FileConverterOption) *pipeline.ProcessorInfo[string] {
	return NewFileConverter("avi", options...)
}

func NewMkvFileConverter(options ...FileConverterOption) *pipeline.ProcessorInfo[string] {
	return NewFileConverter("mkv", options...)
}

func FileConvertWithDestPath(destPath string) FileConverterOption {
	return func(p *FileConverterProcessor) {
		p.destPath = destPath
	}
}

func FileConverterWithDeleteSource(delete bool) FileConverterOption {
	return func(p *FileConverterProcessor) {
		p.deleteSource = delete
	}
}

func FileConverterWithTimeout(timeout time.Duration) FileConverterOption {
	return func(fcp *FileConverterProcessor) {
		fcp.initTimeout = timeout
	}
}
