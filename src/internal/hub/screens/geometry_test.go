package screens

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"sort"
	"testing"

	"github.com/LanceLRQ/PiMon/src/pkg/model"
)

// fixtureCase 是 testdata/grid_cases.json 的一个用例；前端 Vitest 读同一份文件。
type fixtureCase struct {
	Name    string     `json:"name"`
	Grid    model.Grid `json:"grid"`
	Widgets []struct {
		ID      string   `json:"id"`
		Col     int      `json:"col"`
		Row     int      `json:"row"`
		W       int      `json:"w"`
		H       int      `json:"h"`
		Allowed []string `json:"allowed"`
	} `json:"widgets"`
	Problems []struct {
		Widget string `json:"widget"`
		Code   string `json:"code"`
		With   string `json:"with"`
	} `json:"problems"`
}

func sortedKeys(ps []model.LayoutProblem) []string {
	out := make([]string, 0, len(ps))
	for _, p := range ps {
		out = append(out, fmt.Sprintf("%s|%s|%s", p.Widget, p.Code, p.With))
	}
	sort.Strings(out)
	return out
}

func TestCheckGeometry共享用例(t *testing.T) {
	raw, err := os.ReadFile("testdata/grid_cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Cases []fixtureCase `json:"cases"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if len(doc.Cases) < 15 {
		t.Fatalf("用例数量异常: %d", len(doc.Cases))
	}
	for _, c := range doc.Cases {
		t.Run(c.Name, func(t *testing.T) {
			ws := make([]Placement, 0, len(c.Widgets))
			for _, w := range c.Widgets {
				ws = append(ws, Placement{ID: w.ID, Col: w.Col, Row: w.Row, W: w.W, H: w.H, Allowed: w.Allowed})
			}
			got := sortedKeys(CheckGeometry(c.Grid, ws))
			want := []string{}
			for _, p := range c.Problems {
				want = append(want, fmt.Sprintf("%s|%s|%s", p.Widget, p.Code, p.With))
			}
			sort.Strings(want)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("problems = %v，期望 %v", got, want)
			}
		})
	}
}
