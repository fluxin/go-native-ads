package ads

func (conn *Connection) SetAdsState(state AdsState) error {
	return conn.writeControl(state, 0, nil)
}
