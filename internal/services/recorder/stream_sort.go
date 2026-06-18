package recorder

import (
	"sort"

	"github.com/bilirec/bilirec/internal/modules/bilibili"
)

// streamProfileRank breaks ties at the same qn: flv → fmp4 → ts.
func streamProfileRank(format string) int {
	switch format {
	case "flv":
		return 0
	case "fmp4":
		return 1
	case "ts":
		return 2
	default:
		return 3
	}
}

func sortStreams(streams []bilibili.StreamURLInfo) {
	sort.SliceStable(streams, func(i, j int) bool {
		if streams[i].Qn != streams[j].Qn {
			return streams[i].Qn > streams[j].Qn
		}
		return streamProfileRank(streams[i].Format) < streamProfileRank(streams[j].Format)
	})
}
