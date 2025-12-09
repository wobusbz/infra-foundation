package cluster

func (x *N2MSend) MessageID() int32                   { return 1 }
func (x *N2MSend) MessageName() string                { return "N2MSend" }
func (x *N2MSend) NodeName() string                   { return "" }
func (x *N2MSend) ModeName() string                   { return "" }
func (x *N2MOnConnection) MessageID() int32           { return 2 }
func (x *N2MOnConnection) MessageName() string        { return "N2MOnConnection" }
func (x *N2MOnConnection) NodeName() string           { return "" }
func (x *N2MOnConnection) ModeName() string           { return "" }
func (x *M2NOnConnection) MessageID() int32           { return 3 }
func (x *M2NOnConnection) MessageName() string        { return "M2NOnConnection" }
func (x *M2NOnConnection) NodeName() string           { return "" }
func (x *M2NOnConnection) ModeName() string           { return "" }
func (x *N2MOnSessionBindServer) MessageID() int32    { return 4 }
func (x *N2MOnSessionBindServer) MessageName() string { return "N2MOnSessionBindServer" }
func (x *N2MOnSessionBindServer) NodeName() string    { return "" }
func (x *N2MOnSessionBindServer) ModeName() string    { return "" }
func (x *N2MOnSessionClose) MessageID() int32         { return 5 }
func (x *N2MOnSessionClose) MessageName() string      { return "N2MOnSessionClose" }
func (x *N2MOnSessionClose) NodeName() string         { return "" }
func (x *N2MOnSessionClose) ModeName() string         { return "" }
