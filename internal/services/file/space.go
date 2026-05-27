package file

import (
	"github.com/bilirec/bilirec/utils"
)

type DiskSpace struct {
	Used  uint64 `json:"used"`
	Total uint64 `json:"total"`
	Free  uint64 `json:"free"`
}

// GetDiskSpace returns disk usage information for the output directory
func (s *Service) GetDiskSpace() (*DiskSpace, error) {
	usage, err := utils.GetDiskSpace(s.cfg.OutputDir)
	if err != nil {
		return nil, err
	}

	return &DiskSpace{
		Used:  usage.Used,
		Total: usage.Total,
		Free:  usage.Free,
	}, nil
}
