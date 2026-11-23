package payment

import (
	"errors"
	"net/url"
	"strings"
	"testing"
)

func validBank() Config {
	return Config{ConfigBankCode: "VCB", ConfigBankName: "Vietcombank", ConfigAccountNumber: "0123456789", ConfigAccountName: "NGUYEN VAN A"}
}

func fieldsOf(t *testing.T, err error) map[string]string {
	t.Helper()
	var vs Violations
	if !errors.As(err, &vs) {
		t.Fatalf("want violations, got %v", err)
	}
	out := map[string]string{}
	for _, v := range vs {
		out[v.Field] = v.Reason
	}
	return out
}

func TestCheckBankConfigKeepsOnlyTheBankFields(t *testing.T) {
	in := validBank()
	in[ConfigBankCode] = " VCB "
	in["extra"] = "ignored"
	in[ConfigPhone] = "0900000000"
	got, err := CheckBankConfig(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 4 || got[ConfigBankCode] != "VCB" || got["extra"] != "" || got[ConfigPhone] != "" {
		t.Errorf("cleaned config: %v", got)
	}
	delete(in, ConfigBankName)
	if got, _ := CheckBankConfig(in); len(got) != 3 {
		t.Errorf("the bank name is optional: %v", got)
	}
}

func TestCheckBankConfigRefusals(t *testing.T) {
	tests := map[string]struct {
		mutate func(Config)
		field  string
		reason string
	}{
		"no bank":        {func(c Config) { delete(c, ConfigBankCode) }, "config.bank_code", ReasonRequired},
		"unknown bank":   {func(c Config) { c[ConfigBankCode] = "XYZ" }, "config.bank_code", ReasonUnknownBank},
		"short account":  {func(c Config) { c[ConfigAccountNumber] = "123" }, "config.account_number", ReasonBadAccountNumber},
		"spaced account": {func(c Config) { c[ConfigAccountNumber] = "0123 4567" }, "config.account_number", ReasonBadAccountNumber},
		"no account":     {func(c Config) { delete(c, ConfigAccountNumber) }, "config.account_number", ReasonRequired},
		"no holder":      {func(c Config) { c[ConfigAccountName] = "  " }, "config.account_name", ReasonRequired},
		"long holder":    {func(c Config) { c[ConfigAccountName] = strings.Repeat("a", 101) }, "config.account_name", ReasonTooLong},
		"long bank name": {func(c Config) { c[ConfigBankName] = strings.Repeat("a", 101) }, "config.bank_name", ReasonTooLong},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			c := validBank()
			tt.mutate(c)
			_, err := CheckBankConfig(c)
			if got := fieldsOf(t, err)[tt.field]; got != tt.reason {
				t.Errorf("%s: %q, want %q", tt.field, got, tt.reason)
			}
		})
	}
	_, err := CheckBankConfig(Config{})
	if n := len(fieldsOf(t, err)); n != 3 {
		t.Errorf("an empty form lists every missing field at once, got %d", n)
	}
}

func TestCheckMomoConfig(t *testing.T) {
	got, err := CheckMomoConfig(Config{ConfigPhone: "0912345678", ConfigAccountName: "A", ConfigBankCode: "VCB"})
	if err != nil || len(got) != 2 {
		t.Fatalf("%v, %v", got, err)
	}
	for _, phone := range []string{"", "912345678", "09123456789", "0abcdefghi"} {
		if _, err := CheckMomoConfig(Config{ConfigPhone: phone, ConfigAccountName: "A"}); err == nil {
			t.Errorf("phone %q should be refused", phone)
		}
	}
}

func TestVietQRURL(t *testing.T) {
	raw := BankInstructions(validBank(), "HD T11-P101 K3T9Q", 1_500_000).QRURL
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if u.Host != "img.vietqr.io" || u.Path != "/image/VCB-0123456789-compact2.png" {
		t.Errorf("address: %s", raw)
	}
	q := u.Query()
	if q.Get("amount") != "1500000" || q.Get("addInfo") != "HD T11-P101 K3T9Q" || q.Get("accountName") != "NGUYEN VAN A" {
		t.Errorf("query: %v", q)
	}
}

func TestBankInstructionsShowTheAccountAndTheTransferText(t *testing.T) {
	i := BankInstructions(validBank(), "HD T11-P101 K3T9Q", 700_000)
	if i.Method != MethodManualProof || i.TransferContent != "HD T11-P101 K3T9Q" || i.AccountNumber != "0123456789" || i.BankName != "Vietcombank" || i.AmountVND != 700_000 {
		t.Errorf("%+v", i)
	}
}

func TestMoMoInstructions(t *testing.T) {
	i := MoMoInstructions(Config{ConfigPhone: "0912345678", ConfigAccountName: "A"}, "CODE", 90_000)
	if i.MoMoLink != "https://nhantien.momo.vn/0912345678/90000" || i.Method != MethodMomo {
		t.Errorf("%+v", i)
	}
	if !strings.HasPrefix(i.QRURL, "https://api.qrserver.com/") || !strings.Contains(i.QRURL, url.QueryEscape(i.MoMoLink)) {
		t.Errorf("QR link: %s", i.QRURL)
	}
}

func TestBanksAreUniqueAndKnown(t *testing.T) {
	seen := map[string]bool{}
	for _, b := range Banks() {
		if seen[b.Code] || b.Label == "" {
			t.Errorf("bank %+v is repeated or has no label", b)
		}
		seen[b.Code] = true
	}
	if !IsBank("MB") || IsBank("mb") || IsBank("") {
		t.Error("bank codes are matched exactly")
	}
}

func TestMaskAccount(t *testing.T) {
	for in, want := range map[string]string{"0123456789": "******6789", "1234": "****", "12": "**", "": ""} {
		if got := MaskAccount(in); got != want {
			t.Errorf("MaskAccount(%q) = %q, want %q", in, got, want)
		}
	}
}
