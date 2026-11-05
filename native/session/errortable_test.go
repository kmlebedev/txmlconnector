package session

import "testing"

// TestInternalErrorTable pins the RU table of errors.md (67 codes) and the
// "{%d} " form of the DLL.
func TestInternalErrorTable(t *testing.T) {
	if len(internalErrorTexts) != 67 {
		t.Fatalf("internal error table = %d codes, want 67", len(internalErrorTexts))
	}
	if text, ok := InternalErrorText(8); !ok || text != "Неверное значение параметра '%s'." {
		t.Fatalf("code 8 = %q, %v", text, ok)
	}
	if text, ok := FormatInternalError(37); !ok || text != "{37} Заявка отменена" {
		t.Fatalf("format 37 = %q, %v", text, ok)
	}
	if _, ok := InternalErrorText(67); ok {
		t.Fatal("code 67 accepted")
	}
	if _, ok := FormatInternalError(999); ok {
		t.Fatal("unknown code formatted")
	}
}

// TestTransactionErrorTexts: RQ_TRANSACT codes 1..6 are O texts; 7+ are I and
// not formatted (Unknown error at the caller).
func TestTransactionErrorTexts(t *testing.T) {
	want := map[byte]string{
		1: "Клиент не допущен к торгам.",
		2: "Трейдер не допущен к торгам.",
		3: "Клиент не обслуживается данным Трейдером.",
		4: "Сделки с собой запрещены.",
		5: "Попытка снять несуществующую заявку.",
		6: "Транзакции не разрешены.",
	}
	for code, text := range want {
		got, ok := TransactionErrorText(code)
		if !ok || got != text {
			t.Fatalf("code %d = %q, %v", code, got, ok)
		}
	}
	if _, ok := TransactionErrorText(7); ok {
		t.Fatal("code 7 unexpectedly mapped (I)")
	}
}
