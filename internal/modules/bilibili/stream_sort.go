package bilibili

import "sort"

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

func sortStreams(streams []StreamURLInfo, qn int) {
	sort.SliceStable(streams, func(i, j int) bool {
		if qn > 0 {
			pi := streams[i].Qn == qn
			pj := streams[j].Qn == qn
			if pi != pj {
				return pi
			}
		}
		if streams[i].Qn != streams[j].Qn {
			return streams[i].Qn > streams[j].Qn
		}
		return streamProfileRank(streams[i].Format) < streamProfileRank(streams[j].Format)
	})
}
