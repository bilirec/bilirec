package room

import (
	"fmt"

	"github.com/eric2788/bilirec/internal/modules/bilibili"
	"github.com/jellydator/ttlcache/v3"
)

var roomtNotFoundMarker = &bilibili.LiveRoomInfoDetail{}

func (r *Service) GetLiveRoomInfo(roomID int) (*bilibili.LiveRoomInfoDetail, error) {
	data := r.cache.Get(fmt.Sprint(roomID))
	if data != nil {
		info := data.Value()
		if info == roomtNotFoundMarker {
			return nil, bilibili.ErrRoomNotFound
		} else {
			return info, nil
		}
	}
	info, err := r.bilic.GetLiveRoomInfo(roomID)
	if err != nil {
		if bilibili.IsErrRoomNotFound(err) {
			r.cache.Set(fmt.Sprint(roomID), roomtNotFoundMarker, ttlcache.DefaultTTL)
		}
		return nil, err
	}
	r.cache.Set(fmt.Sprint(roomID), info, ttlcache.DefaultTTL)
	return info, nil
}

func (r *Service) IsRoomLive(roomID int) (bool, error) {
	info, err := r.GetLiveRoomInfo(roomID)
	if err != nil {
		return false, err
	}
	return info.LiveStatus == 1, nil
}

func (r *Service) GetMultipleRoomInfos(roomIDs ...int) (map[string]*bilibili.LiveRoomInfoDetail, error) {
	infos := make(map[string]*bilibili.LiveRoomInfoDetail)
	missedIDs := make([]int, 0, len(roomIDs))

	// Check cache first
	for _, id := range roomIDs {
		idStr := fmt.Sprint(id)
		if data := r.cache.Get(idStr); data != nil {
			info := data.Value()
			if info != roomtNotFoundMarker {
				infos[idStr] = info
			} else {
				// earily return if any room not found
				return nil, bilibili.ErrRoomNotFound
			}
		} else {
			missedIDs = append(missedIDs, id)
		}
	}

	// Fetch missing ones in batches of 100
	const batchSize = 100
	for i := 0; i < len(missedIDs); i += batchSize {
		end := i + batchSize
		if end > len(missedIDs) {
			end = len(missedIDs)
		}
		fetchedInfos, err := r.bilic.GetLiveRoomInfos(missedIDs[i:end]...)
		if err != nil {
			// no caching since we don't know which one failed
			return nil, err
		}
		for id, info := range fetchedInfos {
			r.cache.Set(id, info, ttlcache.DefaultTTL)
			infos[id] = info
		}
	}

	return infos, nil
}
