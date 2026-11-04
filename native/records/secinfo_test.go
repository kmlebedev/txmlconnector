package records_test

import (
	"compress/gzip"
	"encoding/hex"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/kmlebedev/txmlconnector/native/internal/oracle"
	"github.com/kmlebedev/txmlconnector/native/model"
	"github.com/kmlebedev/txmlconnector/native/records"
	"github.com/kmlebedev/txmlconnector/native/xmlcompat"
)

// readGz loads a fixture of native/testdata (gzip of hex or text).
func readGz(t *testing.T, name string) []byte {
	t.Helper()
	f, err := os.Open("../testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	data, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func loadHexGz(t *testing.T, name string) []byte {
	t.Helper()
	payload, err := hex.DecodeString(strings.TrimSpace(string(readGz(t, name))))
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return payload
}

// TestSecInfoMatchesDLL renders <sec_info> from the RQ_SEC_SPEC replies of the
// r11 capture and compares with the 5 DLL callbacks (SBER, bond, SiZ6, SBER,
// SiZ6). The catalog identity/seccode/market/decimals/point_cost/lot_volume and
// the accruedint of the bond are seeded from the expected callbacks and the S
// facts of native/testdata/secinfo-live.md: the recorded main1 chunk does not
// contain the bond/future secids, and accruedint is not part of RQ_SEC_SPEC
// (I; needs further RE). Everything else is parsed from the fixture.
func TestSecInfoMatchesDLL(t *testing.T) {
	payload := loadHexGz(t, "secinfo-live.hex.gz")
	replies, err := records.ParseSecSpec(payload)
	if err != nil {
		t.Fatalf("ParseSecSpec: %v", err)
	}
	if len(replies) != 3 {
		t.Fatalf("spec replies = %d, want 3", len(replies))
	}
	want := strings.Split(strings.TrimSpace(string(readGz(t, "secinfo-dll.xml.gz"))), "\n")
	if len(want) != 5 {
		t.Fatalf("callbacks = %d, want 5", len(want))
	}
	// The fixture also carries harness log lines after the callbacks: cut them.
	for i, line := range want {
		if end := strings.Index(line, "</sec_info>"); end >= 0 {
			line = line[:end+len("</sec_info>")]
		}
		want[i] = line
	}
	// Identity seeds (S): secid -> secname-independent fields.
	type seed struct {
		code     string
		market   byte
		kind     byte
		decimals byte
		point    float64
		lot      int32
	}
	seeds := map[uint16]seed{
		26:    {"SBER", 1, 1, 2, 1, 0},
		10043: {"SU26224RMFS4", 1, 2, 4, 0.1, 0},
		20583: {"SiZ6", 4, 4, 0, 100, 1000},
	}
	catalog := oracle.LoadCatalog(t, "main1-demo.hex.gz")
	ids := []uint16{26, 10043, 20583}
	specs := map[uint16]records.SecSpec{}
	for _, r := range replies {
		specs[r.SecID] = r
	}
	for _, id := range ids {
		s := seeds[id]
		catalog.AddSecurity(records.SecNew{
			SecID: id, SecCode: s.code, Market: s.market, Kind: s.kind,
			Pits: []records.Pit{{Board: 1, Decimals: s.decimals, PointCost: s.point, LotSize: s.lot, Default: true}},
		})
	}
	order := []uint16{26, 10043, 20583, 26, 20583}
	for i, id := range order {
		spec, ok := specs[id]
		if !ok {
			t.Fatalf("no spec for %d", id)
		}
		sec, _ := catalog.Security(id)
		info := spec.Info(sec)
		if id == 10043 {
			// accruedint is not in RQ_SEC_SPEC (I): seed it from the DLL line.
			v := model.Decimal{Units: 3082, Scale: 2}
			info.AccruedInt = &v
		}
		if id == 10043 && info.MatDate == nil {
			t.Fatal("bond mat date is empty")
		}
		got, ok := xmlcompat.Render(catalog, &info, xmlcompat.Options{})
		if !ok {
			t.Fatalf("secid %d has no XML form", id)
		}
		if got != want[i] {
			t.Fatalf("sec_info %d\n got %s\nwant %s", i, got, want[i])
		}
	}
}
