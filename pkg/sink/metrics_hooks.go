package sink

type Hooks struct {
	OnQueueBytes func(delta int)
	OnDropped    func()
	OnFailed     func(error)
}
