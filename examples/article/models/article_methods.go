package models

import "strings"

// NormalizeTitle is application-owned behavior promoted by the canonical
// project facade. Generated lifecycle methods remain owned by the facade.
func (article *Article) NormalizeTitle() {
	if article == nil {
		return
	}
	article.Title = strings.TrimSpace(article.Title)
}
