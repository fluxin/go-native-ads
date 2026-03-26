package ads

import "testing"

func TestValidateTimeTypeAcceptsTimeAdsTypes(t *testing.T) {
	types := []string{"TOD", "TIME_OF_DAY", "DATE", "DT", "DATE_AND_TIME"}
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

func TestValidateDurationTypeAcceptsTIME(t *testing.T) {
	if err := validateDurationType("TIME", nil); err != nil {
		t.Fatalf("validateDurationType(TIME) returned error: %v", err)
	}
}

func TestValidateDurationTypeRejectsNonDurationAdsTypes(t *testing.T) {
	for _, tt := range []string{"INT", "TOD", "DATE", "DT"} {
		if err := validateDurationType(tt, nil); err == nil {
			t.Fatalf("expected error for %q", tt)
		}
	}
}
