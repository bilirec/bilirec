package room

import (
	"fmt"
	"strconv"

	"github.com/eric2788/bilirec/internal/modules/bilibili"
)

var roomtNotFoundMarker = &bilibili.LiveRoomInfoDetail{}

func (r *Service) GetLiveRoomInfo(roomID int) (*bilibili.LiveRoomInfoDetail, error) {
	key := fmt.Sprint(roomID)
	if info, ok, stale := r.cache.Get(key); ok {
		if stale {
			r.cache.RevalidateAsync(key, r.getFreshRoomInfo(roomID))
		}
		if info == roomtNotFoundMarker {
			return nil, bilibili.ErrRoomNotFound
		}
		return info, nil
	}

	info, err := r.cache.Load(key, r.getFreshRoomInfo(roomID))
	if err != nil {
		return nil, err
	}
	if info == roomtNotFoundMarker {
		return nil, bilibili.ErrRoomNotFound
	}
	return info, nil
}

func (r *Service) InvalidateRooms(roomIDs ...int) {
	for _, id := range roomIDs {
		r.cache.Delete(fmt.Sprint(id))
	}
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
	staleIDs := make([]int, 0)

	// Check cache first
	for _, id := range roomIDs {
		idStr := fmt.Sprint(id)
		if info, ok, stale := r.cache.Get(idStr); ok {
			if info != roomtNotFoundMarker {
				infos[idStr] = info
				if stale {
					staleIDs = append(staleIDs, id)
				}
			}
		} else {
			missedIDs = append(missedIDs, id)
		}
	}

	if len(missedIDs) > 0 {
		if err := r.fetchAndStoreRoomInfos(missedIDs); err != nil {
			// no caching since we don't know which one failed
			return nil, err
		}
		for _, id := range missedIDs {
			idStr := fmt.Sprint(id)
			info, ok, _ := r.cache.Get(idStr)
			if ok && info != roomtNotFoundMarker {
				infos[idStr] = info
			}
		}
	}

	if len(staleIDs) > 0 {
		r.refreshRoomInfosInBackground(staleIDs)
	}

	return infos, nil
}

func (r *Service) fetchAndStoreRoomInfos(roomIDs []int) error {
	const batchSize = 100
	for i := 0; i < len(roomIDs); i += batchSize {
		end := min(i+batchSize, len(roomIDs))
		batch := roomIDs[i:end]
		fetchedInfos, err := r.bilic.GetLiveRoomInfos(batch...)
		if err != nil {
			if bilibili.IsErrRoomNotFound(err) {
				for _, id := range batch {
					r.cache.Set(fmt.Sprint(id), roomtNotFoundMarker)
				}
				continue
			}
			return err
		}
		for id, info := range fetchedInfos {
			r.cache.Set(id, info)
		}
		for _, id := range batch {
			idStr := strconv.Itoa(id)
			if _, ok := fetchedInfos[idStr]; !ok {
				r.cache.Set(idStr, roomtNotFoundMarker)
			}
		}
	}
	return nil
}

func (r *Service) refreshRoomInfosInBackground(roomIDs []int) {
	if len(roomIDs) == 0 {
		return
	}
	ids := append([]int(nil), roomIDs...)
	go func() {
		if err := r.fetchAndStoreRoomInfos(ids); err != nil {
			logger.Debugf("background revalidation failed for rooms %v: %v", ids, err)
		}
	}()
}

func (r *Service) getFreshRoomInfo(roomID int) func() (*bilibili.LiveRoomInfoDetail, error) {
	return func() (*bilibili.LiveRoomInfoDetail, error) {
		fetched, err := r.bilic.GetLiveRoomInfo(roomID)
		if err != nil {
			if bilibili.IsErrRoomNotFound(err) {
				return roomtNotFoundMarker, nil
			}
			return nil, err
		}
		return fetched, nil
	}
}
