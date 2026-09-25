package ads

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"reflect"
	"time"
)

func (conn *Connection) fetchMetadata() (map[string]SymbolUploadDataType, map[string]*Symbol, error) {
	uploadInfo, err := conn.getSymbolUploadInfo()
	if err != nil {
		return nil, nil, fmt.Errorf("symbol upload info: %w", err)
	}
	datatypesResponse, err := conn.getUploadSymbolInfoDataTypes(uploadInfo.DataTypeLength)
	if err != nil {
		return nil, nil, fmt.Errorf("datatype upload: %w", err)
	}
	datatypes, err := parseUploadSymbolInfoDataTypes(datatypesResponse)
	if err != nil {
		return nil, nil, fmt.Errorf("parse datatypes: %w", err)
	}
	symbolsResponse, err := conn.getUploadSymbolInfoSymbols(uploadInfo.SymbolLength)
	if err != nil {
		return nil, nil, fmt.Errorf("symbol upload: %w", err)
	}
	symbols, err := parseUploadSymbolInfoSymbols(symbolsResponse, datatypes)
	if err != nil {
		return nil, nil, fmt.Errorf("parse symbols: %w", err)
	}
	return datatypes, symbols, nil
}

func (conn *Connection) GetSymbol(symbolName string) (*Symbol, error) {
	if err := conn.beginOperation(); err != nil {
		return nil, err
	}
	defer conn.endOperation()
	return conn.lookupSymbol(symbolName, true)
}

// Acquisition does not hold the notification/metadata mutex over network I/O.
type handleAcquisition struct {
	done   chan struct{}
	symbol *Symbol
	err    error
}

func cloneSymbol(symbol *Symbol) *Symbol {
	symbolCacheLock.Lock()
	defer symbolCacheLock.Unlock()
	copy := *symbol
	return &copy
}
func (conn *Connection) lookupSymbol(name string, internal bool) (*Symbol, error) {
	conn.symbolLock.Lock()
	symbol, ok := conn.symbols[name]
	if !ok {
		conn.symbolLock.Unlock()
		return nil, fmt.Errorf("symbol %s does not exist", name)
	}
	if symbol.Handle != 0 {
		copy := cloneSymbol(symbol)
		conn.symbolLock.Unlock()
		return copy, nil
	}
	if conn.acquisitions == nil {
		conn.acquisitions = make(map[string]*handleAcquisition)
	}
	if pending := conn.acquisitions[name]; pending != nil {
		conn.symbolLock.Unlock()
		select {
		case <-pending.done:
			return pending.symbol, pending.err
		case <-conn.ctx.Done():
			return nil, conn.ctx.Err()
		}
	}
	pending := &handleAcquisition{done: make(chan struct{})}
	conn.acquisitions[name] = pending
	copy := cloneSymbol(symbol)
	conn.symbolLock.Unlock()
	resp, err := conn.writeRead(uint32(GroupSymbolHandleByName), 0, 4, append([]byte(name), 0), internal)
	if err == nil {
		copy.Handle = binary.LittleEndian.Uint32(resp)
		if copy.Handle == 0 {
			err = fmt.Errorf("server returned zero symbol handle")
		}
	}
	conn.symbolLock.Lock()
	if err == nil {
		conn.symbols[name] = copy
		pending.symbol = copy
	}
	pending.err = err
	delete(conn.acquisitions, name)
	close(pending.done)
	conn.symbolLock.Unlock()
	return pending.symbol, err
}
func (conn *Connection) datatypeSnapshot() map[string]SymbolUploadDataType {
	conn.symbolLock.Lock()
	defer conn.symbolLock.Unlock()
	return conn.datatypes
}

func (conn *Connection) getSymbolUploadInfo() (uploadInfo SymbolUploadInfo, err error) {
	res, err := conn.read(uint32(GroupSymbolUploadInfo2), 0, 24, true) //UploadSymbolInfo;
	if err != nil {
		return uploadInfo, fmt.Errorf("failed to get symbol upload info: %w", err)
	}
	buf := bytes.NewBuffer(res)
	err = binary.Read(buf, binary.LittleEndian, &uploadInfo)
	if err != nil {
		return uploadInfo, fmt.Errorf("failed to parse symbol upload info: %w", err)
	}
	return uploadInfo, nil
}

func (conn *Connection) getUploadSymbolInfoSymbols(length uint32) (data []byte, err error) {
	res, err := conn.read(uint32(GroupSymbolUpload), 0, length, true) //UploadSymbolInfo;
	if err != nil {
		return nil, fmt.Errorf("failed to get symbol info symbols: %w", err)
	}
	return res, nil
}

func (conn *Connection) getUploadSymbolInfoDataTypes(length uint32) (data []byte, err error) {
	data, err = conn.read(
		uint32(GroupSymbolDataTypeUpload),
		0x0,
		length, true)
	if err != nil {
		return nil, fmt.Errorf("error doing DT UPLOAD %d", err)
	}
	return data, nil
}

// Update represents a typed notification update.
// Use Subscribe[T]() to receive updates through a channel.
type Update[T any] struct {
	Variable  string
	Value     T
	TimeStamp time.Time
}

// parseNotificationTimestamp converts Windows FILETIME to Go time.Time
func parseNotificationTimestamp(timestamp uint64) time.Time {
	timeStamp := int64(timestamp)/windowsTick - secToUnixEpoch
	return time.Unix(timeStamp, int64(timestamp)%(windowsTick)*100)
}

// createNotificationCallback creates a callback function that decodes notification data
// and sends it to the provided channel. This makes the notification flow explicit.
func createNotificationCallback[T any](symbol *Symbol, types map[string]SymbolUploadDataType, updates chan<- Update[T]) NotificationCallback {
	plan, err := codecFor(reflect.TypeFor[T](), symbol, types)
	if err != nil {
		return func(context.Context, uint64, []byte) error { return err }
	}
	return notificationCallback(symbol, plan, updates)
}
func notificationCallback[T any](symbol *Symbol, plan *codecNode, updates chan<- Update[T]) NotificationCallback {
	return func(ctx context.Context, timestamp uint64, content []byte) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if len(content) != plan.size {
			return fmt.Errorf("notification length mismatch for %s", symbol.FullName)
		}
		var value T
		if err := plan.decode(reflect.ValueOf(&value).Elem(), content); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case updates <- Update[T]{Variable: symbol.FullName, Value: value, TimeStamp: parseNotificationTimestamp(timestamp)}:
			return nil
		}
	}
}
