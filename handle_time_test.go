package ads

import "testing"

func TestValidateTimeTypeAcceptsTimeAdsTypes(t *testing.T) {
	types := []string{"TIME", "TOD", "TIME_OF_DAY", "DATE", "DT", "DATE_AND_TIME"}
	for _, tt := range types {
		if err := validateTimeType(tt, nil); err != nil {
			t.Fatalf("validateTimeType(%q) returned error: %v", tt, err)
		}
	}
}

func TestValidateTimeTypeRejectsNonTimeAdsTypes(t *testing.T) {
	if err := validateTimeType("INT", nil); err == nil {
		t.Fatal("expected error for INT")
	}
}
