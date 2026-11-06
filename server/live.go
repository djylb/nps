package server

// ClientConnected reads live bridge membership without refreshing or sorting lists.
func ClientConnected(id int) bool {
	if Bridge == nil || Bridge.Client == nil {
		return false
	}
	_, ok := Bridge.Client.Load(id)
	return ok
}
