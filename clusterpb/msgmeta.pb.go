package clusterpb

func (x *N2MOnHandshake) MessageID() int32              { return 1 }
func (x *N2MOnHandshake) MessageName() string           { return "N2MOnHandshake" }
func (x *N2MOnHandshake) ModelName() string             { return "cluster" }
func (x *N2MOnHandshake) ServiceName() string           { return "cluster" }
func (x *N2MOnSessionBind) MessageID() int32            { return 2 }
func (x *N2MOnSessionBind) MessageName() string         { return "N2MOnSessionBind" }
func (x *N2MOnSessionBind) ModelName() string           { return "cluster" }
func (x *N2MOnSessionBind) ServiceName() string         { return "cluster" }
func (x *N2MOnSessionDisconnected) MessageID() int32    { return 3 }
func (x *N2MOnSessionDisconnected) MessageName() string { return "N2MOnSessionDisconnected" }
func (x *N2MOnSessionDisconnected) ModelName() string   { return "cluster" }
func (x *N2MOnSessionDisconnected) ServiceName() string { return "cluster" }
func (x *N2MOnPush) MessageID() int32                   { return 4 }
func (x *N2MOnPush) MessageName() string                { return "N2MOnPush" }
func (x *N2MOnPush) ModelName() string                  { return "cluster" }
func (x *N2MOnPush) ServiceName() string                { return "cluster" }
