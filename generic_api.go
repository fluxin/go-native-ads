package ads

// GetHandle binds a typed symbol. The package function remains source compatible.
func (conn *Connection) GetHandle[T any](name string) (*Handle[T], error) {
	return GetHandle[T](conn, name)
}

// NewBatchReader binds a typed sum-read destination.
func (conn *Connection) NewBatchReader[T any](handles ...any) (*BatchReader[T], error) {
	return NewBatchReader[T](conn, handles...)
}

// NewBatchWriter binds a typed sum-write source.
func (conn *Connection) NewBatchWriter[T any](handles ...any) (*BatchWriter[T], error) {
	return NewBatchWriter[T](conn, handles...)
}
