package fake

import (
	"context"
	"testing"
)

func TestParseSize(t *testing.T) {
	tests := []struct {
		value string
		want  int64
		ok    bool
	}{
		{"2G", 2 << 30, true},
		{"1.5G", 1536 << 20, true},
		{"512M", 512 << 20, true},
		{"4096", 4096, true},
		{"8k", 8 << 10, true},
		// zfs prints these for a dataset with no quota, and they are not a
		// number: reading one as a limit would invent a quota nobody set.
		{"none", 0, false},
		{"-", 0, false},
		{"", 0, false},
		{"lots", 0, false},
		{"-1G", 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			got, ok := parseSize(tt.value)
			if ok != tt.ok {
				t.Fatalf("parseSize(%q) ok = %v, want %v", tt.value, ok, tt.ok)
			}
			if ok && got != tt.want {
				t.Errorf("parseSize(%q) = %d, want %d", tt.value, got, tt.want)
			}
		})
	}
}

// A refquota is the whole reason JD-007 puts a jail's limit on a child dataset,
// so a fake that reported the pool's free space for a quota'd dataset would be
// hiding the one thing worth seeing about it.
func TestDatasetsUnder_RefquotaCapsAvailable(t *testing.T) {
	system := NewStorageSystem()
	ctx := context.Background()

	if _, err := system.CreateDataset(ctx, "zroot/jails/nginx/data",
		map[string]string{"refquota": "2G"}); err != nil {
		t.Fatalf("CreateDataset() error = %v", err)
	}

	datasets, err := system.DatasetsUnder(ctx, []string{"zroot/jails/nginx"})
	if err != nil {
		t.Fatalf("DatasetsUnder() error = %v", err)
	}

	var found, sibling bool
	for _, d := range datasets {
		switch d.Name {
		case "zroot/jails/nginx/data":
			found = true
			if want := int64(2<<30) - d.Referenced; d.Available != want {
				t.Errorf("available = %d, want %d (the quota less what it refers to)", d.Available, want)
			}
		case "zroot/jails/nginx":
			sibling = true
			// No quota on the jail itself, so it still reports the pool's.
			if d.Available != 400<<30 {
				t.Errorf("an unquota'd dataset reported %d available, want the pool's 400G", d.Available)
			}
		}
	}
	if !found || !sibling {
		t.Fatalf("datasets = %+v, want both the jail and its quota'd child", datasets)
	}
}
