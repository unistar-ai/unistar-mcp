package server

import "testing"

func TestPaginateLines(t *testing.T) {
	text := "a\nb\nc\nd\ne"
	page, total, next, hasMore := paginateLines(text, 0, 2)
	if total != 5 || page != "a\nb" || next != 2 || !hasMore {
		t.Fatalf("page0: total=%d next=%d hasMore=%v page=%q", total, next, hasMore, page)
	}
	page, total, next, hasMore = paginateLines(text, 2, 2)
	if total != 5 || page != "c\nd" || next != 4 || !hasMore {
		t.Fatalf("page1: total=%d next=%d hasMore=%v page=%q", total, next, hasMore, page)
	}
	page, total, next, hasMore = paginateLines(text, 4, 2)
	if total != 5 || page != "e" || next != 5 || hasMore {
		t.Fatalf("page2: total=%d next=%d hasMore=%v page=%q", total, next, hasMore, page)
	}
	_, total, next, hasMore = paginateLines(text, 5, 2)
	if total != 5 || next != 5 || hasMore {
		t.Fatalf("past end: total=%d next=%d hasMore=%v", total, next, hasMore)
	}
}
