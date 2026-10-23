package blog

import "strings"

// NormalizeTitle is application-owned behavior promoted by the canonical
// project facade. Generated lifecycle methods remain owned by the facade.
func (post *Post) NormalizeTitle() {
	if post == nil {
		return
	}
	post.Title = strings.TrimSpace(post.Title)
}
