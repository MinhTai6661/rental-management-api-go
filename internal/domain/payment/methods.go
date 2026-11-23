package payment

import (
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Bank is a bank a landlord can receive transfers in: the code VietQR uses in its image address, and the name shown.
type Bank struct {
	Code  string
	Label string
}

// Banks lists the banks a landlord can pick (the ones the VietQR service draws a code for).
func Banks() []Bank {
	return []Bank{
		{"VCB", "VCB – Vietcombank"}, {"TCB", "TCB – Techcombank"}, {"MB", "MB – MB Bank"}, {"ACB", "ACB – ACB"},
		{"BIDV", "BIDV – BIDV"}, {"ICB", "ICB – VietinBank"}, {"VBA", "VBA – Agribank"}, {"VPB", "VPB – VPBank"},
		{"TPB", "TPB – TPBank"}, {"STB", "STB – Sacombank"}, {"HDB", "HDB – HDBank"}, {"VIB", "VIB – VIB"},
		{"SHB", "SHB – SHB"}, {"OCB", "OCB – OCB"}, {"MSB", "MSB – MSB"}, {"EIB", "EIB – Eximbank"},
		{"SEAB", "SEAB – SeABank"}, {"LPB", "LPB – LPBank"}, {"CAKE", "CAKE – CAKE by VPBank"}, {"TIMO", "TIMO – Timo"},
	}
}

// IsBank says whether the code is one of Banks.
func IsBank(code string) bool {
	for _, b := range Banks() {
		if b.Code == code {
			return true
		}
	}
	return false
}

// The fields of the settings of the payment methods.
const (
	ConfigBankCode      = "bank_code"
	ConfigBankName      = "bank_name"
	ConfigAccountNumber = "account_number"
	ConfigAccountName   = "account_name"
	ConfigPhone         = "phone"
)

const (
	maxBankNameLength    = 100
	maxAccountNameLength = 100
	// configPrefix names the fields of a method's settings in a violation ("config.bank_code").
	configPrefix = "config."
)

var (
	accountNumberPattern = regexp.MustCompile(`^[0-9A-Za-z]{4,30}$`)
	phonePattern         = regexp.MustCompile(`^0[0-9]{9}$`)
)

// checkAccountName collects the problems of the account holder's name and returns it cleaned.
func checkAccountName(c Config, vs *Violations) string {
	name := strings.TrimSpace(c[ConfigAccountName])
	switch {
	case name == "":
		*vs = append(*vs, violation(configPrefix+ConfigAccountName, ReasonRequired, nil)...)
	case utf8.RuneCountInString(name) > maxAccountNameLength:
		*vs = append(*vs, violation(configPrefix+ConfigAccountName, ReasonTooLong, map[string]string{"max": strconv.Itoa(maxAccountNameLength)})...)
	}
	return name
}

// CheckBankConfig checks the settings of a bank transfer (the bank, the account number and the account holder) and
// returns them cleaned: only the fields of a bank account, without the spaces around them.
func CheckBankConfig(c Config) (Config, error) {
	var vs Violations
	code := strings.TrimSpace(c[ConfigBankCode])
	switch {
	case code == "":
		vs = append(vs, violation(configPrefix+ConfigBankCode, ReasonRequired, nil)...)
	case !IsBank(code):
		vs = append(vs, violation(configPrefix+ConfigBankCode, ReasonUnknownBank, nil)...)
	}
	bankName := strings.TrimSpace(c[ConfigBankName])
	if utf8.RuneCountInString(bankName) > maxBankNameLength {
		vs = append(vs, violation(configPrefix+ConfigBankName, ReasonTooLong, map[string]string{"max": strconv.Itoa(maxBankNameLength)})...)
	}
	number := strings.TrimSpace(c[ConfigAccountNumber])
	switch {
	case number == "":
		vs = append(vs, violation(configPrefix+ConfigAccountNumber, ReasonRequired, nil)...)
	case !accountNumberPattern.MatchString(number):
		vs = append(vs, violation(configPrefix+ConfigAccountNumber, ReasonBadAccountNumber, nil)...)
	}
	name := checkAccountName(c, &vs)
	if len(vs) > 0 {
		return nil, vs
	}
	clean := Config{ConfigBankCode: code, ConfigAccountNumber: number, ConfigAccountName: name}
	if bankName != "" {
		clean[ConfigBankName] = bankName
	}
	return clean, nil
}

// CheckMomoConfig checks the settings of a MoMo wallet (the phone number and the account holder) and returns them
// cleaned.
func CheckMomoConfig(c Config) (Config, error) {
	var vs Violations
	phone := strings.TrimSpace(c[ConfigPhone])
	switch {
	case phone == "":
		vs = append(vs, violation(configPrefix+ConfigPhone, ReasonRequired, nil)...)
	case !phonePattern.MatchString(phone):
		vs = append(vs, violation(configPrefix+ConfigPhone, ReasonBadPhone, nil)...)
	}
	name := checkAccountName(c, &vs)
	if len(vs) > 0 {
		return nil, vs
	}
	return Config{ConfigPhone: phone, ConfigAccountName: name}, nil
}

// Instructions is what a tenant needs to make a payment, shown next to the bill. The QR code is a link to a picture
// another service draws from these values: nothing here fetches or stores it.
type Instructions struct {
	Method string
	// AmountVND is what the QR code asks for: what is still due, or the amount the tenant is about to pay.
	AmountVND     int64
	BankCode      string
	BankName      string
	AccountNumber string
	AccountName   string
	// Phone and MoMoLink belong to a MoMo wallet.
	Phone    string
	MoMoLink string
	// TransferContent is the text to write on the transfer: the transfer text of the bill.
	TransferContent string
	QRURL           string
}

// VietQRURL is the address of the picture the VietQR service draws for a bank account, with the amount and the
// transfer text already filled in.
func VietQRURL(bankCode, account string, amountVND int64, transferText, accountName string) string {
	q := url.Values{}
	q.Set("amount", strconv.FormatInt(amountVND, 10))
	q.Set("addInfo", transferText)
	q.Set("accountName", accountName)
	return fmt.Sprintf("https://img.vietqr.io/image/%s-%s-compact2.png?%s", url.PathEscape(bankCode), url.PathEscape(account), q.Encode())
}

// BankInstructions tells how to pay into a bank account the landlord set up.
func BankInstructions(c Config, paymentCode string, amountVND int64) Instructions {
	return Instructions{
		Method: MethodManualProof, AmountVND: amountVND, BankCode: c[ConfigBankCode], BankName: c[ConfigBankName],
		AccountNumber: c[ConfigAccountNumber], AccountName: c[ConfigAccountName], TransferContent: paymentCode,
		QRURL: VietQRURL(c[ConfigBankCode], c[ConfigAccountNumber], amountVND, paymentCode, c[ConfigAccountName]),
	}
}

// MoMoLink is MoMo's "receive money" link: opening it (or scanning the QR code of it) starts a transfer to the number.
func MoMoLink(phone string, amountVND int64) string {
	return fmt.Sprintf("https://nhantien.momo.vn/%s/%d", url.PathEscape(phone), amountVND)
}

// MoMoInstructions tells how to pay into a MoMo wallet the landlord set up.
func MoMoInstructions(c Config, paymentCode string, amountVND int64) Instructions {
	link := MoMoLink(c[ConfigPhone], amountVND)
	return Instructions{
		Method: MethodMomo, AmountVND: amountVND, Phone: c[ConfigPhone], AccountName: c[ConfigAccountName],
		TransferContent: paymentCode, MoMoLink: link,
		QRURL: "https://api.qrserver.com/v1/create-qr-code/?size=300x300&margin=8&data=" + url.QueryEscape(link),
	}
}

// MaskAccount hides all but the last four characters of an account number (for the audit trail).
func MaskAccount(s string) string {
	r := []rune(s)
	if len(r) <= 4 {
		return strings.Repeat("*", len(r))
	}
	return strings.Repeat("*", len(r)-4) + string(r[len(r)-4:])
}
