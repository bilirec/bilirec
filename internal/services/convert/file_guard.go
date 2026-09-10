package convert

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/bilirec/bilirec/pkg/ffmpeg"
	"github.com/bilirec/bilirec/pkg/logger"
	"github.com/bilirec/bilirec/pkg/mp4"
)

const minimumExportedFileBytesRequired int64 = 1 * 1024 * 1024

func isConvertedFileInvalid(outputBytes, inputBytes int64) bool {
	if outputBytes < minimumExportedFileBytesRequired {
		return true
	}
	if inputBytes > 0 && outputBytes*2 < inputBytes {
		return true
	}
	return false
}

func validateOutputFileSize(inputPath, outputPath string) error {
	output, err := os.Stat(outputPath)
	if err != nil {
		return fmt.Errorf("获取转码输出 %s 文件状态失败：%w", outputPath, err)
	}

	input, err := os.Stat(inputPath)
	inputBytes := int64(0)
	if err == nil {
		inputBytes = input.Size()
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("获取源文件 %s 状态失败：%w", inputPath, err)
	}

	outputBytes := output.Size()
	if !isConvertedFileInvalid(outputBytes, inputBytes) {
		return nil
	}

	return fmt.Errorf("转码输出过小：output=%dB，input=%dB", outputBytes, inputBytes)
}

func validateOutputContainer(ctx context.Context, taskLog logger.Logger, file, format string) error {
	err := ffmpeg.Probe(ctx, taskLog,
		"-v", "error",
		"-f", format,
		"-i", file,
		"-show_entries", "format=format_name",
		"-of", "csv=p=0",
	)
	if err == nil {
		return nil
	}
	if !errors.Is(err, ffmpeg.ErrProbeUnavailable) {
		return fmt.Errorf("ffprobe 校验输出失败：%w", err)
	}
	if !mp4.IsISOBMFF(format) {
		return fmt.Errorf("无法校验输出：ffprobe 不可用且格式 %s 不是 ISO-BMFF", format)
	}
	ok, err := mp4.HasMoov(file)
	if err != nil {
		return fmt.Errorf("扫描输出 moov 失败：%w", err)
	}
	if !ok {
		return fmt.Errorf("转码输出缺少 moov：%s", file)
	}
	return nil
}
