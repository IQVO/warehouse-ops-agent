package approval

import "testing"

func TestDecodeIsStrictAboutShapeAndTypes(t *testing.T) {
	good := `["release_next_work","pick-zone-a","deterministic","r","abc",1793016300]`
	if _, err := decode([]byte(good)); err != nil {
		t.Fatalf("a well-formed payload must decode: %v", err)
	}
	for name, bad := range map[string]string{
		"too few fields":    `["release_next_work","p","deterministic","r","abc"]`,
		"too many fields":   `["release_next_work","p","deterministic","r","abc",1,2]`,
		"string expiry":     `["release_next_work","p","deterministic","r","abc","1793016300"]`,
		"fractional expiry": `["release_next_work","p","deterministic","r","abc",1793016300.5]`,
		"numeric path":      `["release_next_work",7,"deterministic","r","abc",1793016300]`,
		"null field":        `["release_next_work",null,"deterministic","r","abc",1793016300]`,
		"object not array":  `{"a":"release_next_work"}`,
		"empty document":    ``,
		"not json":          `release_next_work`,
	} {
		if _, err := decode([]byte(bad)); err == nil {
			t.Errorf("%s: decode succeeded, want ErrInvalidToken", name)
		}
	}
}
