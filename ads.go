package ads

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
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
	if err := conn.ensureConnected(); err != nil {
		return nil, err
	}

	conn.symbolLock.Lock()
	defer conn.symbolLock.Unlock()
	localSymbol, ok := conn.symbols[symbolName]
	if ok {
		if localSymbol.Handle == 0 {
			// Get handle by name inline
			resp, err := conn.WriteRead(uint32(GroupSymbolHandleByName), 0, 4, []byte(symbolName))
			if err != nil {
				slog.Error("error getting handle by name", "error", err, "symbol name", symbolName)
				return nil, err
			}
			localSymbol.Handle = binary.LittleEndian.Uint32(resp)
		}
		slog.Debug("symbol got", "symbol", localSymbol)
		return localSymbol, nil
	}
	err := fmt.Errorf("symbol does not exist")
	slog.Error("error getting symbol", "error", err, "symbol name", symbolName)
	return nil, err
}

func (conn *Connection) getSymbolUploadInfo() (uploadInfo SymbolUploadInfo, err error) {
	res, err := conn.Read(uint32(GroupSymbolUploadInfo2), 0, 24) //UploadSymbolInfo;
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
	res, err := conn.Read(uint32(GroupSymbolUpload), 0, length) //UploadSymbolInfo;
	if err != nil {
		return nil, fmt.Errorf("failed to get symbol info symbols: %w", err)
	}
	return res, nil
}

func (conn *Connection) getUploadSymbolInfoDataTypes(length uint32) (data []byte, err error) {
	data, err = conn.Read(
		uint32(GroupSymbolDataTypeUpload),
		0x0,
		length)
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
func createNotificationCallback[T any](symbol *Symbol, datatypes map[string]SymbolUploadDataType, updateChan chan<- Update[T]) NotificationCallback {
	return func(ctx context.Context, timestamp uint64, content []byte) error {
		notificationTime := parseNotificationTimestamp(timestamp)

		var value T
		val := reflect.ValueOf(&value).Elem()

		// Decode based on kind
		switch val.Kind() {
		case reflect.Struct:
			if err := decodeStructValue(val, symbol, content, datatypes); err != nil {
				slog.Error("Failed to decode struct notification", "symbol", symbol.FullName, "error", err)
				return err
			}
		case reflect.Array:
			if err := decodeArrayField(val, symbol, content, datatypes); err != nil {
				slog.Error("Failed to decode array notification", "symbol", symbol.FullName, "error", err)
				return err
			}
		default:
			if err := decodePrimitiveField(val, symbol, content, datatypes); err != nil {
				slog.Error("Failed to decode primitive notification", "symbol", symbol.FullName, "error", err)
				return err
			}
		}

		// Send to channel with context cancellation support
		select {
		case <-ctx.Done():
			return ctx.Err()
		case updateChan <- Update[T]{
			Variable:  symbol.FullName,
			Value:     value,
			TimeStamp: notificationTime,
		}:
			slog.Debug("Successfully delivered notification", "symbol", symbol.FullName)
		}

		return nil
	}
}
