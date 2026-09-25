package ads

import (
	"fmt"
	"reflect"
)

type symbolBinding struct {
	conn       *Connection
	handle     uint32
	length     uint32
	dataType   string
	symbolName string
	bindEpoch  uint64
	symbol     *Symbol
	typ        reflect.Type
	codec      *codecNode
}

func (info *symbolBinding) bind() error {
	epoch := info.conn.CurrentEpoch()
	if info.bindEpoch == epoch && info.handle != 0 && (info.typ == nil || info.codec != nil) {
		return nil
	}
	symbol, err := info.conn.lookupSymbol(info.symbolName, true)
	if err != nil {
		return err
	}
	if uint64(symbol.Length)+40 > uint64(info.conn.frameLimit()) {
		return fmt.Errorf("symbol exceeds frame limit")
	}
	var plan *codecNode
	if info.typ != nil {
		plan, err = codecFor(info.typ, symbol, info.conn.datatypeSnapshot())
		if err != nil {
			return err
		}
	}
	info.handle = symbol.Handle
	info.length = symbol.Length
	info.dataType = symbol.DataType
	info.bindEpoch = epoch
	info.symbol = symbol
	info.codec = plan
	return nil
}
