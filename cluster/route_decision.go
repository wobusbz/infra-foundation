package cluster

import (
	"infra-foundation/session"
)

type RouteKind int

const (
	RouteLocalModel RouteKind = iota
	RouteFrontendClient
	RouteBackendNode
	RouteGatewayNode
	RouteDrop
)

type Direction int

const (
	DirClientInbound Direction = iota
	DirClusterInbound
)

type RouteDecision struct {
	Kind     RouteKind
	TargetID string
	Service  string
}

func (mr *MessageRouter) Decide(msgID int32, s session.Identity, dir Direction) RouteDecision {
	if mr.dispatcher != nil && mr.dispatcher.IsLocalHandler(msgID) {
		return RouteDecision{Kind: RouteLocalModel}
	}

	isFrontend := mr.sessionBinder.isLocalFrontend()
	_, isWriter := s.(FrontendWriter)

	if mr.registry.HasRoute(msgID) {
		switch dir {
		case DirClientInbound:
			return RouteDecision{Kind: RouteBackendNode, Service: mr.registry.GetServiceByRoute(msgID)}
		case DirClusterInbound:
			if isWriter && isFrontend {
				return RouteDecision{Kind: RouteFrontendClient}
			}
			return RouteDecision{Kind: RouteDrop}
		}
	}

	if isWriter && isFrontend {
		return RouteDecision{Kind: RouteFrontendClient}
	}

	return RouteDecision{Kind: RouteGatewayNode}
}
