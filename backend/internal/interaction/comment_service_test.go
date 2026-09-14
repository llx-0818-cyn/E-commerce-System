package interaction

import (
	"reflect"
	"testing"
)

func TestParseMentionsNormalizesAndDeduplicates(t *testing.T) {
	got := ParseMentions("你好 @Alice，重复 @alice，再提及 @张三 和 @team_lead")
	want := []string{"alice", "张三", "team_lead"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseMentions() = %#v, want %#v", got, want)
	}
}
