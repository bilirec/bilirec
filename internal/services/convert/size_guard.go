package convert

import (
	"fmt"
	"os"
)

const minimumConvertedOutputBytes int64 = 1 * 1024 * 1024

func isConvertedFileInvalid(outputBytes, inputBytes int64) bool {
	if outputBytes < minimumConvertedOutputBytes {
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
		return fmt.Errorf("stat converted output %s: %w", outputPath, err)
	}

	input, err := os.Stat(inputPath)
	inputBytes := int64(0)
	if err == nil {
		inputBytes = input.Size()
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat source file %s: %w", inputPath, err)
	}

	outputBytes := output.Size()
	if !isConvertedFileInvalid(outputBytes, inputBytes) {
		return nil
	}

	return fmt.Errorf("converted output too small: output=%dB, input=%dB", outputBytes, inputBytes)
}
