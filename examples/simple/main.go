package main

// Regenerate enum wrappers used by this smoke test:
//go:generate go run ../../cmd/codegen/main.go -symbols=MAIN.theetest -o generated_enums.go -pkg main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	ads "codeberg.org/fluxin/go-native-ads"
)

type TestStruct struct {
	I           ads.Int16       `ads:"i"`
	B           ads.Float32     `ads:"b"`
	TestArraySt [13]ads.Float64 `ads:"test_array_st"`
}

type Numba struct {
	More    ads.Float64 `ads:"More"`
	Dizasta ads.Int16   `ads:"Dizasta"`
	Bleeks  ads.Uint32  `ads:"bleeks"`
	Moar    TestStruct  `ads:"moar"`
}

type BatchData struct {
	ChangeInt ads.Int16
	B         ads.Float32
	State     ads.Uint16
	Start     ads.Bool
}

type testCase struct {
	name string
	fn   func(*ads.Connection) error
}

var (
	ip        = flag.String("ip", "127.0.0.1", "AMS router address")
	netid     = flag.String("netid", "localhost", "target AMS NetID (use 'localhost' for local)")
	port      = flag.Int("port", 48898, "AMS router TCP port")
	testRoute = flag.Bool("test-route-helper", false, "run UDP NetID discovery smoke check")
)

func main() {
	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	conn, err := ads.NewConnection(ctx, ads.ConnectionOptions{
		IP:      *ip,
		Port:    *port,
		NetID:   *netid,
		AMSPort: 851,
	})
	if err != nil {
		failf("create connection: %v", err)
	}

	if err := conn.Connect(); err != nil {
		failf("connect: %v", err)
	}
	defer conn.Close()

	fmt.Println("Go Native ADS - Comprehensive Smoke Test")
	fmt.Println("========================================")
	fmt.Printf("Target: %s (%s:851)\n", *ip, *netid)

	tests := []testCase{
		{name: "Device information and ADS state", fn: testDeviceAndState},
		{name: "Symbol version read/refresh path", fn: testSymbolVersionRefresh},
		{name: "Primitive scalar round-trips", fn: testPrimitives},
		{name: "Enum round-trip (TESTE)", fn: testEnum},
		{name: "Time values (DATE/TIME/TOD/DT)", fn: testTimeValues},
		{name: "Struct round-trips (TestStruct, NUMBA)", fn: testStructs},
		{name: "INT array and element handles", fn: testIntArray},
		{name: "ArrayOfStructs field coverage", fn: testArrayOfStructs},
		{name: "ArrayOfStructs whole-array read/write", fn: testArrayOfStructsWholeArray},
		{name: "Batch read/write", fn: testBatchReadWrite},
		{name: "Notifications (on-change and cyclic)", fn: testNotifications},
		{name: "Type safety and expected failures", fn: testTypeSafety},
	}
	if *testRoute {
		tests = append(tests, testCase{name: "Route helper: NetID discovery", fn: testRouteHelperNetID})
	}

	passed := 0
	for i, tc := range tests {
		fmt.Printf("\n[%d/%d] %s\n", i+1, len(tests), tc.name)
		err := tc.fn(conn)
		if err != nil {
			fmt.Printf("  FAIL: %v\n", err)
			continue
		}
		passed++
		fmt.Println("  PASS")
	}

	fmt.Printf("\nResult: %d/%d tests passed\n", passed, len(tests))
	if passed != len(tests) {
		os.Exit(1)
	}
}

func testDeviceAndState(conn *ads.Connection) error {
	state, err := conn.ReadState()
	if err != nil {
		return err
	}

	info, err := conn.ReadDeviceInfo()
	if err != nil {
		return err
	}

	name := strings.TrimRight(string(info.DeviceName[:]), "\x00")
	fmt.Printf("  Device: %s (%d.%d.%d)\n", name, info.Major, info.Minor, info.Version)
	fmt.Printf("  ADS state: %d, device state: %d\n", state.AdsState, state.DeviceState)
	return nil
}

func testSymbolVersionRefresh(conn *ads.Connection) error {
	versionBefore, err := conn.ReadSymbolVersion()
	if err != nil {
		return fmt.Errorf("read symbol version (initial): %w", err)
	}
	fmt.Printf("  Current symbol version: %d\n", versionBefore)
	fmt.Println("  Change the IO tree (or reactivate the PLC project), then press ENTER to continue...")
	if err := waitForEnter(); err != nil {
		return err
	}
	time.Sleep(250 * time.Millisecond)

	versionAfter, err := conn.ReadSymbolVersion()
	if err != nil {
		return fmt.Errorf("read symbol version (after change): %w", err)
	}
	if versionAfter == versionBefore {
		return fmt.Errorf("symbol version did not change (%d). change IO tree/reactivate before continuing", versionAfter)
	}

	h, err := ads.GetHandle[ads.Int16](conn, "MAIN.change_int")
	if err != nil {
		return err
	}
	if err := writeReadExact(h, ads.Int16(-321)); err != nil {
		return fmt.Errorf("post-version-handle check: %w", err)
	}

	fmt.Printf("  Symbol version: %d -> %d\n", versionBefore, versionAfter)
	return nil
}

func waitForEnter() error {
	reader := bufio.NewReader(os.Stdin)
	_, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("failed waiting for enter: %w", err)
	}
	return nil
}

func testPrimitives(conn *ads.Connection) error {
	changeInt, err := ads.GetHandle[ads.Int16](conn, "MAIN.change_int")
	if err != nil {
		return err
	}
	b, err := ads.GetHandle[ads.Float32](conn, "MAIN.b")
	if err != nil {
		return err
	}
	state, err := ads.GetHandle[ads.Uint16](conn, "MAIN.state")
	if err != nil {
		return err
	}
	start, err := ads.GetHandle[ads.Bool](conn, "MAIN.start")
	if err != nil {
		return err
	}
	longint, err := ads.GetHandle[ads.Int64](conn, "MAIN.longint")
	if err != nil {
		return err
	}
	lword, err := ads.GetHandle[ads.Uint64](conn, "MAIN.lwordtest")
	if err != nil {
		return err
	}
	ulint, err := ads.GetHandle[ads.Uint64](conn, "MAIN.ulintdsaaa")
	if err != nil {
		return err
	}
	dastring, err := ads.GetHandle[ads.String](conn, "MAIN.dastring")
	if err != nil {
		return err
	}

	if err := writeReadExact(changeInt, ads.Int16(-1234)); err != nil {
		return fmt.Errorf("MAIN.change_int: %w", err)
	}
	if err := writeReadApprox(b, ads.Float32(123.375), 0.001); err != nil {
		return fmt.Errorf("MAIN.b: %w", err)
	}
	if err := writeReadExact(state, ads.Uint16(65530)); err != nil {
		return fmt.Errorf("MAIN.state: %w", err)
	}
	if err := writeReadExact(start, ads.Bool(true)); err != nil {
		return fmt.Errorf("MAIN.start: %w", err)
	}
	if err := writeReadExact(longint, ads.Int64(-9223372036854775000)); err != nil {
		return fmt.Errorf("MAIN.longint: %w", err)
	}
	if err := writeReadExact(lword, ads.Uint64(18446744073709550000)); err != nil {
		return fmt.Errorf("MAIN.lwordtest: %w", err)
	}
	if err := writeReadExact(ulint, ads.Uint64(9000000000000)); err != nil {
		return fmt.Errorf("MAIN.ulintdsaaa: %w", err)
	}
	if err := writeReadExact(dastring, ads.String("Go ADS smoke")); err != nil {
		return fmt.Errorf("MAIN.dastring: %w", err)
	}

	cycleCounter, err := ads.GetHandle[ads.Int16](conn, "MAIN.i")
	if err != nil {
		return err
	}
	v1, err := cycleCounter.Read()
	if err != nil {
		return err
	}
	time.Sleep(100 * time.Millisecond)
	v2, err := cycleCounter.Read()
	if err != nil {
		return err
	}
	if v2 < v1 {
		return fmt.Errorf("MAIN.i should be monotonic, got %d then %d", v1, v2)
	}

	fmt.Printf("  MAIN.i changed from %d to %d\n", v1, v2)
	return nil
}

func testEnum(conn *ads.Connection) error {
	theetest, err := ads.GetHandle[TESTE](conn, "MAIN.theetest")
	if err != nil {
		return err
	}

	if err := writeReadExact(theetest, TESTE(0)); err != nil {
		return fmt.Errorf("enum 0: %w", err)
	}
	if err := writeReadExact(theetest, TESTE(1)); err != nil {
		return fmt.Errorf("enum 1: %w", err)
	}
	return nil
}

func testTimeValues(conn *ads.Connection) error {
	today, err := ads.GetHandle[time.Time](conn, "MAIN.today")
	if err != nil {
		return err
	}
	timeVar, err := ads.GetHandle[time.Duration](conn, "MAIN.timetest")
	if err != nil {
		return err
	}
	todVar, err := ads.GetHandle[time.Time](conn, "MAIN.blargh")
	if err != nil {
		return err
	}
	dtVar, err := ads.GetHandle[time.Time](conn, "MAIN.dt_test")
	if err != nil {
		return err
	}

	dateRef := time.Date(2026, 3, 21, 0, 0, 0, 0, time.UTC)
	if err := today.Write(dateRef); err != nil {
		return err
	}
	dateRead, err := today.Read()
	if err != nil {
		return err
	}
	if dateRead.Unix() != dateRef.Unix() {
		return fmt.Errorf("MAIN.today mismatch: wrote %s, read %s", dateRef, dateRead)
	}

	dtRef := time.Date(2026, 3, 21, 11, 22, 33, 0, time.UTC)
	if err := dtVar.Write(dtRef); err != nil {
		return err
	}
	dtRead, err := dtVar.Read()
	if err != nil {
		return err
	}
	if dtRead.Unix() != dtRef.Unix() {
		return fmt.Errorf("MAIN.dt_test mismatch: wrote %s, read %s", dtRef, dtRead)
	}

	timeRef := 4 * time.Second // T#4s in TwinCAT
	if err := timeVar.Write(timeRef); err != nil {
		return err
	}
	timeRead, err := timeVar.Read()
	if err != nil {
		return err
	}
	if timeRead != timeRef {
		return fmt.Errorf("MAIN.timetest mismatch: wrote %s, read %s", timeRef, timeRead)
	}

	todRef := time.Date(2026, 3, 21, 8, 9, 10, 0, time.UTC)
	if err := todVar.Write(todRef); err != nil {
		return err
	}
	todRead, err := todVar.Read()
	if err != nil {
		return err
	}
	if expected := expectedTodTimeDecode(todRef); !todRead.Equal(expected) {
		return fmt.Errorf("MAIN.blargh mismatch: expected %s, read %s", expected, todRead)
	}

	return nil
}

func testStructs(conn *ads.Connection) error {
	eeks, err := ads.GetHandle[TestStruct](conn, "MAIN.eeks")
	if err != nil {
		return err
	}
	beeks, err := ads.GetHandle[TestStruct](conn, "MAIN.beeks")
	if err != nil {
		return err
	}
	masta, err := ads.GetHandle[Numba](conn, "MAIN.masta_disasta")
	if err != nil {
		return err
	}

	eeksValue, err := eeks.Read()
	if err != nil {
		return err
	}
	fmt.Printf("  MAIN.eeks observed: i=%d, b=%.2f\n", eeksValue.I, eeksValue.B)

	var beeksExpected TestStruct
	beeksExpected.I = 333
	beeksExpected.B = 44.5
	for i := range beeksExpected.TestArraySt {
		beeksExpected.TestArraySt[i] = ads.Float64(float64(i) * 1.25)
	}

	if err := beeks.Write(beeksExpected); err != nil {
		return err
	}
	beeksRead, err := beeks.Read()
	if err != nil {
		return err
	}
	if !equalTestStruct(beeksRead, beeksExpected) {
		return fmt.Errorf("MAIN.beeks mismatch\nexpected: %+v\nactual:   %+v", beeksExpected, beeksRead)
	}

	var mastaExpected Numba
	mastaExpected.More = 9876.54321
	mastaExpected.Dizasta = 1
	mastaExpected.Bleeks = 42424242
	mastaExpected.Moar = beeksExpected
	if err := masta.Write(mastaExpected); err != nil {
		return err
	}
	mastaRead, err := masta.Read()
	if err != nil {
		return err
	}
	if !equalNumba(mastaRead, mastaExpected) {
		return fmt.Errorf("MAIN.masta_disasta mismatch\nexpected: %+v\nactual:   %+v", mastaExpected, mastaRead)
	}

	return nil
}

func testIntArray(conn *ads.Connection) error {
	arrHandle, err := ads.GetHandle[[13]ads.Int16](conn, "MAIN.test_array")
	if err != nil {
		return err
	}
	idx0, err := ads.GetHandle[ads.Int16](conn, "MAIN.test_array[0]")
	if err != nil {
		return err
	}
	idx5, err := ads.GetHandle[ads.Int16](conn, "MAIN.test_array[5]")
	if err != nil {
		return err
	}
	idx12, err := ads.GetHandle[ads.Int16](conn, "MAIN.test_array[12]")
	if err != nil {
		return err
	}

	var expected [13]ads.Int16
	for i := range expected {
		expected[i] = ads.Int16((i + 1) * 10)
	}
	if err := arrHandle.Write(expected); err != nil {
		return err
	}
	actual, err := arrHandle.Read()
	if err != nil {
		return err
	}
	if actual != expected {
		return fmt.Errorf("MAIN.test_array full mismatch")
	}

	if err := writeReadExact(idx0, ads.Int16(101)); err != nil {
		return fmt.Errorf("index 0: %w", err)
	}
	if err := writeReadExact(idx5, ads.Int16(505)); err != nil {
		return fmt.Errorf("index 5: %w", err)
	}
	if err := writeReadExact(idx12, ads.Int16(1212)); err != nil {
		return fmt.Errorf("index 12: %w", err)
	}

	verify, err := arrHandle.Read()
	if err != nil {
		return err
	}
	if verify[0] != 101 || verify[5] != 505 || verify[12] != 1212 {
		return fmt.Errorf("MAIN.test_array element mismatch after element writes: %v", verify)
	}

	return nil
}

func testArrayOfStructs(conn *ads.Connection) error {
	for plcIndex := 1; plcIndex <= 5; plcIndex++ {
		prefix := fmt.Sprintf("MAIN.ArrayOfStructs[%d]", plcIndex)

		more, err := ads.GetHandle[ads.Float64](conn, prefix+".More")
		if err != nil {
			return err
		}
		dizasta, err := ads.GetHandle[ads.Int16](conn, prefix+".Dizasta")
		if err != nil {
			return err
		}
		bleeks, err := ads.GetHandle[ads.Uint32](conn, prefix+".bleeks")
		if err != nil {
			return err
		}
		moarI, err := ads.GetHandle[ads.Int16](conn, prefix+".moar.i")
		if err != nil {
			return err
		}
		moarB, err := ads.GetHandle[ads.Float32](conn, prefix+".moar.b")
		if err != nil {
			return err
		}
		moarArray0, err := ads.GetHandle[ads.Float64](conn, prefix+".moar.test_array_st[0]")
		if err != nil {
			return err
		}

		scale := float64(plcIndex)
		if err := writeReadExact(more, ads.Float64(11.11*scale)); err != nil {
			return fmt.Errorf("%s.More: %w", prefix, err)
		}
		if err := writeReadExact(dizasta, ads.Int16(plcIndex%2)); err != nil {
			return fmt.Errorf("%s.Dizasta: %w", prefix, err)
		}
		if err := writeReadExact(bleeks, ads.Uint32(1000+plcIndex)); err != nil {
			return fmt.Errorf("%s.bleeks: %w", prefix, err)
		}
		if err := writeReadExact(moarI, ads.Int16(700+plcIndex)); err != nil {
			return fmt.Errorf("%s.moar.i: %w", prefix, err)
		}
		if err := writeReadApprox(moarB, ads.Float32(3.5*float32(plcIndex)), 0.001); err != nil {
			return fmt.Errorf("%s.moar.b: %w", prefix, err)
		}
		if err := writeReadExact(moarArray0, ads.Float64(9.25*scale)); err != nil {
			return fmt.Errorf("%s.moar.test_array_st[0]: %w", prefix, err)
		}
	}

	return nil
}

func testArrayOfStructsWholeArray(conn *ads.Connection) error {
	arrHandle, err := ads.GetHandle[[5]Numba](conn, "MAIN.ArrayOfStructs")
	if err != nil {
		return err
	}

	var expected [5]Numba
	for i := range expected {
		scale := float64(i + 1)
		expected[i] = Numba{
			More:    ads.Float64(11.11 * scale),
			Dizasta: ads.Int16((i + 1) % 2),
			Bleeks:  ads.Uint32(1000 + uint32(i) + 1),
			Moar: TestStruct{
				I: ads.Int16(700 + int16(i) + 1),
				B: ads.Float32(3.5 * float32(i+1)),
			},
		}
		expected[i].Moar.TestArraySt[0] = ads.Float64(9.25 * scale)
	}

	if err := arrHandle.Write(expected); err != nil {
		return fmt.Errorf("write whole array: %w", err)
	}

	actual, err := arrHandle.Read()
	if err != nil {
		return fmt.Errorf("read whole array: %w", err)
	}

	for i := range expected {
		if !equalNumba(actual[i], expected[i]) {
			return fmt.Errorf("element [%d] mismatch\nexpected: %+v\nactual:   %+v", i, expected[i], actual[i])
		}
	}

	return nil
}

func testBatchReadWrite(conn *ads.Connection) error {
	changeInt, err := ads.GetHandle[ads.Int16](conn, "MAIN.change_int")
	if err != nil {
		return err
	}
	b, err := ads.GetHandle[ads.Float32](conn, "MAIN.b")
	if err != nil {
		return err
	}
	state, err := ads.GetHandle[ads.Uint16](conn, "MAIN.state")
	if err != nil {
		return err
	}
	start, err := ads.GetHandle[ads.Bool](conn, "MAIN.start")
	if err != nil {
		return err
	}

	writer, err := ads.NewBatchWriter[BatchData](conn, changeInt, b, state, start)
	if err != nil {
		return err
	}
	reader, err := ads.NewBatchReader[BatchData](conn, changeInt, b, state, start)
	if err != nil {
		return err
	}

	expected := BatchData{
		ChangeInt: 4321,
		B:         17.75,
		State:     321,
		Start:     true,
	}
	if err := writer.Write(expected); err != nil {
		return err
	}

	var actual BatchData
	if err := reader.Read(&actual); err != nil {
		return err
	}

	if actual.ChangeInt != expected.ChangeInt {
		return fmt.Errorf("batch ChangeInt mismatch: expected %d got %d", expected.ChangeInt, actual.ChangeInt)
	}
	if math.Abs(float64(actual.B-expected.B)) > 0.001 {
		return fmt.Errorf("batch B mismatch: expected %.3f got %.3f", expected.B, actual.B)
	}
	if actual.State != expected.State {
		return fmt.Errorf("batch State mismatch: expected %d got %d", expected.State, actual.State)
	}
	if actual.Start != expected.Start {
		return fmt.Errorf("batch Start mismatch: expected %v got %v", expected.Start, actual.Start)
	}

	return nil
}

func testNotifications(conn *ads.Connection) error {
	changeInt, err := ads.GetHandle[ads.Int16](conn, "MAIN.change_int")
	if err != nil {
		return err
	}
	counter, err := ads.GetHandle[ads.Int16](conn, "MAIN.i")
	if err != nil {
		return err
	}

	onChangeUpdates := make(chan ads.Update[ads.Int16], 4)
	onChangeSub, err := changeInt.Subscribe(onChangeUpdates, &ads.SubscribeOptions{
		Mode:      ads.TransModeServerOnChange,
		CycleTime: 20 * time.Millisecond,
		MaxDelay:  20 * time.Millisecond,
	})
	if err != nil {
		return err
	}
	defer onChangeSub.Cancel()

	if err := changeInt.Write(1111); err != nil {
		return err
	}
	if _, err := waitForUpdate(onChangeUpdates, 3*time.Second); err != nil {
		return fmt.Errorf("on-change update 1: %w", err)
	}

	if err := changeInt.Write(2222); err != nil {
		return err
	}
	update, err := waitForUpdate(onChangeUpdates, 3*time.Second)
	if err != nil {
		return fmt.Errorf("on-change update 2: %w", err)
	}
	if update.Value != 2222 {
		return fmt.Errorf("unexpected on-change value: %d", update.Value)
	}

	cyclicUpdates := make(chan ads.Update[ads.Int16], 8)
	cyclicSub, err := counter.Subscribe(cyclicUpdates, &ads.SubscribeOptions{
		Mode:      ads.TransModeServerCycle,
		CycleTime: 30 * time.Millisecond,
		MaxDelay:  30 * time.Millisecond,
	})
	if err != nil {
		return err
	}
	defer cyclicSub.Cancel()

	first, err := waitForUpdate(cyclicUpdates, 3*time.Second)
	if err != nil {
		return fmt.Errorf("cyclic first update: %w", err)
	}
	second, err := waitForUpdate(cyclicUpdates, 3*time.Second)
	if err != nil {
		return fmt.Errorf("cyclic second update: %w", err)
	}
	if second.TimeStamp.Before(first.TimeStamp) {
		return fmt.Errorf("cyclic timestamp order invalid: %s then %s", first.TimeStamp, second.TimeStamp)
	}

	return nil
}

func testTypeSafety(conn *ads.Connection) error {
	if _, err := ads.GetHandle[ads.Float32](conn, "MAIN.change_int"); err == nil {
		return errors.New("expected type mismatch for MAIN.change_int as Float32")
	}

	if _, err := ads.GetHandle[ads.Int16](conn, "MAIN.does_not_exist"); err == nil {
		return errors.New("expected missing symbol failure")
	}

	return nil
}

func writeReadExact[T comparable](h *ads.Handle[T], value T) error {
	if err := h.Write(value); err != nil {
		return err
	}
	actual, err := h.Read()
	if err != nil {
		return err
	}
	if actual != value {
		return fmt.Errorf("expected %v, got %v", value, actual)
	}
	return nil
}

func writeReadApprox(h *ads.Handle[ads.Float32], value ads.Float32, tolerance float64) error {
	if err := h.Write(value); err != nil {
		return err
	}
	actual, err := h.Read()
	if err != nil {
		return err
	}
	if math.Abs(float64(actual-value)) > tolerance {
		return fmt.Errorf("expected %.6f, got %.6f", value, actual)
	}
	return nil
}

func waitForUpdate[T any](updates <-chan ads.Update[T], timeout time.Duration) (ads.Update[T], error) {
	select {
	case update := <-updates:
		return update, nil
	case <-time.After(timeout):
		var zero ads.Update[T]
		return zero, fmt.Errorf("timed out after %s", timeout)
	}
}

func expectedTodTimeDecode(input time.Time) time.Time {
	midnight := time.Date(input.Year(), input.Month(), input.Day(), 0, 0, 0, 0, input.Location())
	ms := input.Sub(midnight).Milliseconds()
	return time.Unix(0, int64(time.Millisecond)*ms-int64(time.Hour))
}

func testRouteHelperNetID(conn *ads.Connection) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	discovered, err := ads.DiscoverNetID(ctx, *ip)
	if err != nil {
		return err
	}
	info, err := ads.DiscoverNetIDInfo(ctx, *ip)
	if err != nil {
		return err
	}
	if info.NetID != discovered {
		return fmt.Errorf("DiscoverNetIDInfo mismatch: direct=%s info=%s", discovered, info.NetID)
	}
	parts := strings.Split(discovered, ".")
	if len(parts) != 6 {
		return fmt.Errorf("discovered netid has %d parts: %s", len(parts), discovered)
	}
	parsed, err := ads.ParseNetID(discovered)
	if err != nil {
		return err
	}
	if roundTrip := ads.FormatNetID(parsed); roundTrip != discovered {
		return fmt.Errorf("netid round-trip mismatch: in=%s out=%s", discovered, roundTrip)
	}

	router := conn.Router()
	diag := router.Diagnostics()
	if diag.RouterAddress == "" || diag.RouterPort == 0 {
		return fmt.Errorf("invalid route diagnostics: %+v", diag)
	}
	state, known, updated := router.StateSnapshot()
	_ = state
	_ = known
	_ = updated

	localNetID, err := router.LocalNetID()
	if err != nil {
		return err
	}
	if _, err := ads.ParseNetID(localNetID); err != nil {
		return fmt.Errorf("invalid local netid from router: %w", err)
	}

	fmt.Printf("  DiscoverNetID(%s) -> %s\n", *ip, discovered)
	fmt.Printf("  DiscoverNetIDInfo(%s) -> %s\n", *ip, info.NetID)
	fmt.Printf("  Router diagnostics: addr=%s port=%d connected=%v stateKnown=%v\n", diag.RouterAddress, diag.RouterPort, diag.Connected, diag.StateKnown)
	fmt.Printf("  Router.LocalNetID() -> %s\n", localNetID)
	return nil
}

func equalTestStruct(a, b TestStruct) bool {
	if a.I != b.I {
		return false
	}
	if math.Abs(float64(a.B-b.B)) > 0.001 {
		return false
	}
	for i := range a.TestArraySt {
		if math.Abs(float64(a.TestArraySt[i]-b.TestArraySt[i])) > 0.000001 {
			return false
		}
	}
	return true
}

func equalNumba(a, b Numba) bool {
	if math.Abs(float64(a.More-b.More)) > 0.000001 {
		return false
	}
	if a.Dizasta != b.Dizasta {
		return false
	}
	if a.Bleeks != b.Bleeks {
		return false
	}
	return equalTestStruct(a.Moar, b.Moar)
}

func failf(format string, args ...any) {
	fmt.Printf("ERROR: "+format+"\n", args...)
	os.Exit(1)
}
