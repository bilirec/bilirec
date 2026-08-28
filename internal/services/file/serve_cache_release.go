package file

import (
	"time"

	"github.com/bilirec/bilirec/internal/modules/config"
	"github.com/bilirec/bilirec/pkg/filecache"
)

type serveCacheSession struct {
	refCount int
	timer    *time.Timer
}

// BeginServeCacheRelease pairs with the returned done callback around SendFile.
// When DROP_FILE_PAGE_CACHE is enabled, drops page cache after the last active
// serve ends and ServeCacheIdleReleaseSecs of idle (zero config uses default 60s).
func (s *Service) BeginServeCacheRelease(fullPath string) (done func()) {
	if config.ReadOnly == nil || !config.ReadOnly.DropFilePageCache() {
		return func() {}
	}
	if s.serveCacheIdleDelay() <= 0 {
		return func() {}
	}

	s.serveCacheMu.Lock()
	sess := s.serveCache[fullPath]
	if sess == nil {
		sess = &serveCacheSession{}
		s.serveCache[fullPath] = sess
	}
	if sess.timer != nil {
		sess.timer.Stop()
		sess.timer = nil
		log.Tracef("serve-cache-release: 取消待释放定时器 path=%s", fullPath)
	}
	sess.refCount++
	refCount := sess.refCount
	s.serveCacheMu.Unlock()
	log.Tracef("serve-cache-release: 开始服务 path=%s refCount=%d", fullPath, refCount)

	return func() {
		s.endServeCacheRelease(fullPath)
	}
}

func (s *Service) endServeCacheRelease(fullPath string) {
	idleDelay := s.serveCacheIdleDelay()
	if idleDelay <= 0 {
		return
	}

	s.serveCacheMu.Lock()
	sess := s.serveCache[fullPath]
	if sess == nil {
		s.serveCacheMu.Unlock()
		return
	}
	sess.refCount--
	refCount := sess.refCount
	if refCount > 0 {
		s.serveCacheMu.Unlock()
		log.Tracef("serve-cache-release: 服务结束 path=%s refCount=%d 仍有活动服务", fullPath, refCount)
		return
	}

	path := fullPath
	log.Tracef("serve-cache-release: 进入空闲释放窗口 path=%s idleDelay=%s", path, idleDelay)
	sess.timer = time.AfterFunc(idleDelay, func() {
		s.serveCacheMu.Lock()
		defer s.serveCacheMu.Unlock()
		active := s.serveCache[path]
		if active == nil || active.refCount > 0 {
			refCount := 0
			if active != nil {
				refCount = active.refCount
			}
			log.Tracef("serve-cache-release: 跳过释放 path=%s refCount=%d", path, refCount)
			return
		}
		if active.timer != nil {
			active.timer.Stop()
			active.timer = nil
		}
		s.dropPageCacheLocked(path)
		delete(s.serveCache, path)
	})
	s.serveCacheMu.Unlock()
}

func (s *Service) dropPageCacheLocked(fullPath string) {
	fn := s.dropPageCacheFn
	if fn == nil {
		fn = filecache.DropFilePageCache
	}
	if err := fn(fullPath); err != nil {
		log.Debugf("serve-cache-release: 释放 page cache 失败 path=%s err=%v", fullPath, err)
	} else {
		log.Debugf("serve-cache-release: 释放 page cache 成功 path=%s", fullPath)
	}
}

func (s *Service) serveCacheIdleDelay() time.Duration {
	if s.serveCacheIdleDelayOverride > 0 {
		return s.serveCacheIdleDelayOverride
	}
	if config.ReadOnly == nil {
		return 0
	}
	secs := config.ReadOnly.ServeCacheIdleReleaseSecs()
	if secs <= 0 {
		return 0
	}
	return time.Duration(secs) * time.Second
}

func (s *Service) stopServeCacheRelease() {
	s.serveCacheMu.Lock()
	defer s.serveCacheMu.Unlock()
	for path, sess := range s.serveCache {
		if sess.timer != nil {
			sess.timer.Stop()
			log.Debugf("serve-cache-release: 停止时取消待释放定时器 path=%s refCount=%d", path, sess.refCount)
		}
		delete(s.serveCache, path)
	}
}
