package survey

import "testing"

// One image with two tags: docker system df -v lists it once per tag.
func TestReviewImageWithTwoTags(t *testing.T) {
	out := "docker|on\n" +
		"img|sha256:4f8a2c1d9e7b0000000000000000000000000000000000000000000000000000|node:22|1.1GB|5 weeks ago|0\n" +
		"img|sha256:4f8a2c1d9e7b0000000000000000000000000000000000000000000000000000|node:lts|1.1GB|5 weeks ago|0\n"
	r := parse(out)
	ids := map[string]int{}
	var total int64
	for _, it := range r.Items {
		ids[it.ID]++
		total += it.Bytes
	}
	for id, n := range ids {
		if n > 1 {
			t.Errorf("id %s appears %d times (total counted %d bytes for one 1.1 GB image); command %q", id, n, total, r.Items[0].Command)
		}
	}
}
