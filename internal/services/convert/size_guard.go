package convert

import (
	"fmt"
	"os"
)

const MinimumExportedFileBytesRequired int64 = 1 * 1024 * 1024

func IsConvertedFileInvalid(outputBytes, inputBytes int64) bool {
	if outputBytes < MinimumExportedFileBytesRequired {
		return true
	}
	if inputBytes > 0 && outputBytes*2 < inputBytes {
		return true
	}
	return false
}

func ValidateOutputFileSize(inputPath, outputPath string) error {
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
	if !IsConvertedFileInvalid(outputBytes, inputBytes) {
		return nil
	}

	return fmt.Errorf("转码输出过小：output=%dB，input=%dB", outputBytes, inputBytes)
}
