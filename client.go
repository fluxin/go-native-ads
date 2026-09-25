package ads

func resolveType(conn *Connection, name string) (string, error) {
	return resolveDataType(name, conn.datatypeSnapshot())
}
