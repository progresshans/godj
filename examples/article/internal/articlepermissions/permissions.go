// Package articlepermissions owns the generated-code-independent permission
// names shared by Article persistence adapters and the operator policy.
package articlepermissions

import "github.com/progresshans/godj/auth"

const (
	ArticleViewPermission   auth.Permission = "godj_conformance.view_article"
	ArticleAddPermission    auth.Permission = "godj_conformance.add_article"
	ArticleChangePermission auth.Permission = "godj_conformance.change_article"
	ArticleDeletePermission auth.Permission = "godj_conformance.delete_article"
)
