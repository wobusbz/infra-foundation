package protos

func (x *C2SLogin) MessageID() int32    { return 100 }
func (x *C2SLogin) MessageName() string { return "C2SLogin" }
func (x *C2SLogin) NodeName() string    { return "GAME" }
func (x *C2SLogin) ModeName() string    { return "user" }
func (x *S2CLogin) MessageID() int32    { return 101 }
func (x *S2CLogin) MessageName() string { return "S2CLogin" }
func (x *S2CLogin) NodeName() string    { return "GAME" }
func (x *S2CLogin) ModeName() string    { return "user" }
func (x *M2NLogin) MessageID() int32    { return 102 }
func (x *M2NLogin) MessageName() string { return "M2NLogin" }
func (x *M2NLogin) NodeName() string    { return "GAME" }
func (x *M2NLogin) ModeName() string    { return "user" }
func (x *N2MLogin) MessageID() int32    { return 103 }
func (x *N2MLogin) MessageName() string { return "N2MLogin" }
func (x *N2MLogin) NodeName() string    { return "GAME" }
func (x *N2MLogin) ModeName() string    { return "user" }
