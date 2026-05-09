package cluster

type DiscoveryEventType int

const (
	NodeAdded DiscoveryEventType = iota
	NodeRemoved
	NodeUpdated
)

type DiscoveryEvent struct {
	Type DiscoveryEventType
	Name string
	ID   string
	Data []byte
}

type DiscoveryHandler interface {
	HandleDiscoveryEvent(ev DiscoveryEvent)
}
