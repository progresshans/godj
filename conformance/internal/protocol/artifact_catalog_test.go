package protocol

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// This catalog is copied from the existing independent expected byte locks.
// Do not derive expected hashes from the current artifact contents.
func TestReferenceArtifactsMatchLockedBytes(t *testing.T) {
	t.Parallel()
	root := conformanceRepositoryRoot(t)
	expected := map[string]struct {
		size   int64
		sha256 string
	}{
		"conformance/contracts/migration-target-plan-manifest.json":                                     {6796, "0636eb512d7de824b79d44d17373b3db4c2a6e6f7c712cc9e803480b33ce0496"},
		"conformance/fixtures/godj-migration-target-plan-not-implemented.json":                          {1707, "dfefb6fd6ca27e5e70dffea002fd07d801792ba7c6a83142dab18b969617bd44"},
		"conformance/fixtures/godj-migration-target-plan-deviation-expected.json":                       {2673, "7e0c04e21237da15ab979d9b4bfec41cf81063c37e7ba5dd753c2dc0bfceb317"},
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-target-plan-oracle.json":          {43516, "dc688e27a727270594b32291e8cff83e1bd929af0a0fcd6fcf9b1f706dba9a7f"},
		"conformance/contracts/api-authentication-manifest.json":                                        {7224, "038d5b694ae16d2464965d2b967830a2b0a4818055b6d906ae5320b5abe122d0"},
		"conformance/contracts/article-admin-manifest.json":                                             {6296, "b4ef4017c8af84e9f7428f541dda5276295f1e9f27c2af1b99bc049b306f451e"},
		"conformance/contracts/article-api-manifest.json":                                               {6618, "5047ca955ba5b2099f0d8bf2f6f0ed09944e2fcc705eb4ccd1c5bd6fa500a4e1"},
		"conformance/contracts/auth-session-manifest.json":                                              {4932, "72d4a054839d0b2aa4723ffb44b6f526c34808b182c39905a92d59890a2c79c5"},
		"conformance/contracts/manifest.json":                                                           {-1, "e395fc862d357b7d45f94fa7d2d15f5a5dfdf8c353db958adc280fd64870b874"},
		"conformance/contracts/migration-command-manifest.json":                                         {6166, "d2846327e4d8cbf82a25568e41b198c67878bb7853958729969eb7077ca4c0e1"},
		"conformance/contracts/migration-definition-source-manifest.json":                               {-1, "b5bc2612f3cfc642397ebff779294aa1cdc1a25b675632d2c7a2e615d47ee7fa"},
		"conformance/contracts/migration-execution-manifest.json":                                       {-1, "1857dcf375ed09f8566798ce662c72a86ef41706e478eef6f208077b156886e9"},
		"conformance/contracts/migration-lifecycle-manifest.json":                                       {-1, "5ec1f6bdf35fddce144d4623134b89be05a9d2b12b06fe72df27a4bc935af0d0"},
		"conformance/contracts/migration-planning-manifest.json":                                        {-1, "f51d737bd68eafae32f7942669b467e3457372873ec536a13491ded60ef27ca6"},
		"conformance/contracts/migration-project-check-manifest.json":                                   {5085, "e689b37098a4b26e4faddbd7c7e8a09d9145526f2b7bd1de7fb6cd5cb139c16b"},
		"conformance/contracts/migration-relation-manifest.json":                                        {7858, "ec90feaf988e5c014a9cc08d00f6744993af146f2e5d5c4cd86d1ed6e18f25a9"},
		"conformance/contracts/migration-restart-manifest.json":                                         {-1, "79dda328b9b65c532178db62f289340a5ffd06445b7095aec5f215134b65c290"},
		"conformance/contracts/migration-state-reconstruction-manifest.json":                            {-1, "85398c217e19dbd77747f2abfeafc5d69f166cab154e49d9e1f0bcf8f91e6d5c"},
		"conformance/contracts/migration-status-manifest.json":                                          {5263, "dcb86295e683ea083cc57dca155284f9b26018d5d5a30c9606141bee8946fcc6"},
		"conformance/contracts/migration-writer-manifest.json":                                          {9227, "90bce609ffb4f771007379495629a31efbf00594dca16f9efe875005e97f1c72"},
		"conformance/contracts/parameter-routing-manifest.json":                                         {4689, "85365b3670df5fa5a0d51241dd958d25816d9a285a5070e416120423793a264e"},
		"conformance/contracts/query-breadth-manifest.json":                                             {11282, "04665808db8f775096c07ac1705e6e10f139ac233f71a10c2892403005245167"},
		"conformance/contracts/query-cache-manifest.json":                                               {-1, "35f808e361d85228fe3048ae2510cf296f3127bee5572ce3ed9e66c6fd3eb3e2"},
		"conformance/contracts/query-expression-manifest.json":                                          {16592, "a32365e72bff2f96d576dc2a6322c703c6f0cf7c277776f6b326eda47cf9de17"},
		"conformance/contracts/relation-manifest.json":                                                  {10770, "791408c2c31864217f63b15218740214e4a850997d1e2b65dbb32b41586ff25b"},
		"conformance/contracts/save-lifecycle-manifest.json":                                            {-1, "6f215f6aee153954dee84d0571cc28529c2d50ee31ee2b9755733db3f9762905"},
		"conformance/contracts/system-state-manifest.json":                                              {16420, "ddae48e95770eacf2e3b761c7c4931b53dbcb65020cc375f624413ac71e0996c"},
		"conformance/contracts/template-form-manifest.json":                                             {7584, "4bc189cf71976b6d2ca301a97c6dfc5ee33463dec936f6c13d4181d56e1b1a41"},
		"conformance/contracts/write-migration-manifest.json":                                           {-1, "b0ba235cb8b83e9b595b2ad3230ea7440d8b6ea74789de27c8a1f6625ecd05bb"},
		"conformance/fixtures/godj-api-authentication-deviation-expected.json":                          {2291, "85a9a8b2261e7265b00a33c2cf5b63b9e5b5cd963b2ac7e894dd77988206fc4b"},
		"conformance/fixtures/godj-api-authentication-not-implemented.json":                             {1746, "9562a10f8d729777d35abf0c852a0e90cc98607bfc375252ecec5933dc625434"},
		"conformance/fixtures/godj-article-admin-deviation-expected.json":                               {915, "47d6f144259fbf82046d1e2821b2bd374c245cda30c084b627c5418500b54fc3"},
		"conformance/fixtures/godj-article-admin-not-implemented.json":                                  {1699, "6cf265bc2b92565791c5f9d75f42fb0f86d5c88e9978df44b25d895538447a46"},
		"conformance/fixtures/godj-article-api-deviation-expected.json":                                 {2003, "54758fdf850d4a61f65b764131a444c276ad7bb311a65d86b8b4a1780c979623"},
		"conformance/fixtures/godj-article-api-not-implemented.json":                                    {1736, "fdb05cf9ff8e257c60b210dff29ec012a834110f22b703664f943e6740c2a27d"},
		"conformance/fixtures/godj-auth-session-deviation-expected.json":                                {1184, "f494fb64ae084d40564eaddad7e30792d5ed2f23799c8670e2a150ddb31a665d"},
		"conformance/fixtures/godj-auth-session-not-implemented.json":                                   {1553, "f55fafcf0d979bfb6ba9c534dd89bf8123d1afd740570e786432ae7468b4f618"},
		"conformance/fixtures/godj-migration-command-not-implemented.json":                              {1838, "8680d5e8ce7cf11604af69da1e96a64f580f64074277a2a015af8ad250bb0016"},
		"conformance/fixtures/godj-migration-definition-source-not-implemented.json":                    {-1, "41ec09d0aba93924fc85fc5b84168ab9124fe2422ab0d86c06228102ad4bf299"},
		"conformance/fixtures/godj-migration-execution-deviation-expected.json":                         {-1, "568495ed3dc5e6f3760c28f1c61c40dc54a63483c5b9c11283bf7ae5a8ac7547"},
		"conformance/fixtures/godj-migration-execution-not-implemented.json":                            {-1, "6416e6e9a854d78b94d4242e6ffd1ed3a72caf3c058e0d9c4a78b0690e1a7a04"},
		"conformance/fixtures/godj-migration-lifecycle-deviation-expected.json":                         {-1, "58e773ac6a2eb52faa6ecec78982e75219c5b978ae8295a8902e8bebe8158f1b"},
		"conformance/fixtures/godj-migration-lifecycle-not-implemented.json":                            {-1, "b743a1e74b828184ce1d046999a2c4358c93b85840be2161c7a8f4896d984722"},
		"conformance/fixtures/godj-migration-planning-not-implemented.json":                             {-1, "a9ef26842cd09e4ae01a21d38399ea27e527b0724a7d3e830ecf6c42a12aca13"},
		"conformance/fixtures/godj-migration-project-check-not-implemented.json":                        {1729, "86e0190cc30cd4cf3cb30d882ace3b1c3e2577fd03cca6fe4684a366e7260680"},
		"conformance/fixtures/godj-migration-relation-not-implemented.json":                             {1846, "f9bd9c47b5ab3f91e3bb2b0ca5bf4fc88c1d612caf8d6051236af6738eef9e24"},
		"conformance/fixtures/godj-migration-restart-not-implemented.json":                              {-1, "31a7df8306e1a14def0d5724b3e60d8938f4e4910cf380de119d47de09892c55"},
		"conformance/fixtures/godj-migration-state-reconstruction-not-implemented.json":                 {-1, "9e7e1e40cb6f33bfc37facb7406d3d85ce86e4fbc3743a538b8d8052598d7ee1"},
		"conformance/fixtures/godj-migration-status-not-implemented.json":                               {1566, "0dd4dd08b13b9497ea541b7de4a85448cf6e0358b899c095c6eaafaf290f6cc6"},
		"conformance/fixtures/godj-migration-writer-deviation-expected.json":                            {7242, "74617f20f72ecd5b26284ae8cffb7a1c408cdef03e0933d457beeb82f9f4718e"},
		"conformance/fixtures/godj-migration-writer-not-implemented.json":                               {1876, "b27563a864fe417df53a20092c44f169829e9798cb2e40348c8dbbdcf4715502"},
		"conformance/fixtures/godj-not-implemented.json":                                                {-1, "f02ea4e01e0ffcc9195d56d69129c5def0591cbcdcb5b07a62d2ec7395fa7874"},
		"conformance/fixtures/godj-parameter-routing-deviation-expected.json":                           {2174, "f9d084d178cccdf5928830813810f4d28c930d4414c73091a06dcd825ed38f60"},
		"conformance/fixtures/godj-parameter-routing-not-implemented.json":                              {1608, "7a7e3f3c433f837fb3240f97a75ef66022cf8887c6322d960dc3291eb48776b1"},
		"conformance/fixtures/godj-query-breadth-not-implemented.json":                                  {1867, "f618ca120d38304f8b06064514ac06e4380a492819ad1f3dbb8627183e1eb969"},
		"conformance/fixtures/godj-query-cache-not-implemented.json":                                    {-1, "5cdec6cbd5440527529b08774673136c079895ab834fe2821a1626000d611d87"},
		"conformance/fixtures/godj-query-expression-not-implemented.json":                               {2465, "7ab556ff1f6b77f5e1d4614d6d752cabd6f3428572558d39007e9cd15972f6c2"},
		"conformance/fixtures/godj-relation-not-implemented.json":                                       {1859, "2450dcb948d7418f06458359c73fa78492df59336f0ff666e11a3ca860bd9209"},
		"conformance/fixtures/godj-save-lifecycle-not-implemented.json":                                 {-1, "5ece667fe6babef5d01059ba4166e1243946176f9672119ae45f4c39c440c726"},
		"conformance/fixtures/godj-system-state-deviation-expected.json":                                {1141, "a2877ae785b937b2b1c9ee3b567a7631403a5b5ca91485d2a6c942066c744869"},
		"conformance/fixtures/godj-system-state-not-implemented.json":                                   {3167, "eff126dff0e7e9375a09722d054a2f663150cea1440241875b82f60650d9aa53"},
		"conformance/fixtures/godj-template-form-deviation-expected.json":                               {1472, "0f9b10539677c07aa18c058f0e78925b3388299be853506324325c2af11d2ff3"},
		"conformance/fixtures/godj-template-form-not-implemented.json":                                  {1863, "b1e426264c53dc4885f70aa0f6d2f2231ade201da0fa9fd980d11400960cc1f5"},
		"conformance/fixtures/godj-write-migration-not-implemented.json":                                {-1, "c565c877278032637b75f99c9490c5e7e02169c8730628069533f16da6d8e707"},
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/SHA256SUMS":                                 {2279, "ad256f0bf1b0322c6480b285c701648954b41bf06ecc6c203d4ba8c6b6c6bf87"},
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/article-admin-oracle.json":                  {17645, "869f871fe826b07442810892197bec2d59e0202e413d327154f6d166b7803378"},
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/auth-session-oracle.json":                   {6916, "9eb0bfd37e7aeabac9250374af250ba0b74d2cf4c657cd2543e5dc9626fc36dc"},
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-command-oracle.json":              {12690, "30b1b5c109c9da98a3fce2236ee9faf1f6fe9f4ae31ebdd640b74728160313ee"},
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-definition-source-oracle.json":    {-1, "61401746ce6b01caac002e7043e0818c1eaec417e31a54a8a16450d860104410"},
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-execution-oracle.json":            {-1, "641c8934fb80c74b59caa544f0ea3c30561e01515e0868c6f22678d69428430e"},
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-lifecycle-oracle.json":            {-1, "7eca1ae6a8768cda7af75a3f8d749469e7fb48fd327aa1591b06c922f87174fc"},
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-planning-oracle.json":             {-1, "7ce2916586b827826079ed6750ccabf6069657be30ad0fe08215eece11fba474"},
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-project-check-oracle.json":        {19971, "8bbf10c02950181a8753a11a40a6a81e816be33d1825a8a2469655d9f65bc0aa"},
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-relation-oracle.json":             {120502, "5beadac7a80d0903d552e0bf9d5fae85b139ce0754d9163184d907fcf0da5968"},
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-restart-oracle.json":              {-1, "90a920a195cd8e1cde1cdab62be0092cfd436e96bb0045cac8259c4d293c0727"},
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-state-reconstruction-oracle.json": {-1, "bce71e26f1e919edbfc2d1acc7de9a3bfb8934efeab6e6656c8bcdc38d19a6a9"},
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-status-oracle.json":               {39478, "5a7a7827b37594b5084a25567fedd65152bfb05b5783cdf9e052bdc4d6d9355f"},
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-writer-oracle.json":               {25980, "9068d0e603d631ac8a4da5c564b1aa1037c0854a0935342e3518812bf452fd41"},
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/oracle.json":                                {-1, "e26450788453d2ec294249fa512df5c518f1e03ca338aaf77d5398ea9668e869"},
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/query-breadth-oracle.json":                  {41943, "0236bdab23ad8d6c9fc3c65a810badcb7048ec5b4da6c8ad7fd5387245cccf94"},
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/query-cache-oracle.json":                    {-1, "d899ba46a6361a35d954cc60ba92d4c9f7b80158b6c7df6fcc2e0bf74f406682"},
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/query-expression-oracle.json":               {87852, "4efa5c26f5f17c77e7ef65a0bbdb00cff72835c9a98642726bd61f5524e1ec6f"},
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/relation-oracle.json":                       {33792, "6b7d138d5b0ec60da13e142117e5c9154be2864491c6e9ec63734f9b7dd08290"},
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/save-lifecycle-oracle.json":                 {-1, "05cad687926b59fc036be398896313c8a1b46af79c1f320054698771085260cb"},
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/system-state.json":                          {37866, "2251157e801295b084a51a7879e496fab528d7360fcb8c55bdd7b0b368862913"},
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/template-form-oracle.json":                  {12873, "968218e75b3244e8f72a9a106e967d4e9ab066db756913d8108b7371d4ecd6fa"},
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/write-migration-oracle.json":                {-1, "35ae758f44d5385d093931dba08c33d63964286eab273332407fae11c14a42ac"},
		"conformance/oracles/drf-3.18.0-django-6.1-sqlite-darwin-arm64/SHA256SUMS":                      {283, "429b5f8a1c7ce554f5fa676b0e5c32fdf528cf4888128063a901f3c4d89cda8a"},
		"conformance/oracles/drf-3.18.0-django-6.1-sqlite-darwin-arm64/api-authentication-oracle.json":  {23698, "73262bd3dbc505a110c4b500920f8f1c4df61be34c29c695343323431dbacef3"},
		"conformance/oracles/drf-3.18.0-django-6.1-sqlite-darwin-arm64/article-api-oracle.json":         {46466, "f63f06ac26a1cedac0ea3e7fe9339b163b2571cdbc2a7fea87f8debef690ab56"},
		"conformance/oracles/drf-3.18.0-django-6.1-sqlite-darwin-arm64/parameter-routing-oracle.json":   {12663, "4aded47e2a0db9524a18625174e8d8815b69911e5310323fbe17bad34899cc53"},
		"conformance/profiles/django-6.1-sqlite-darwin-arm64.json":                                      {879, "8b557bf935575f5366f4ebdc07441a8f4a3e2097f8af4a42450eb0fde12a5041"},
		"conformance/profiles/drf-3.18.0-django-6.1-sqlite-darwin-arm64.json":                           {916, "6c0243b8ad398cca45e1ae1edfd99c321bd75e5ef6d0763cef76a5193c99ef1f"},
		"conformance/reference/drf/pyproject.toml":                                                      {319, "46b3482056a64d2c9ac84320f047089c9c406d14d8bec7cf0e7a7b43f71be8b3"},
		"conformance/reference/drf/uv.lock":                                                             {4199, "efc431a1585aaecd9099d40194980771b395bbe261370619f29b5ccf58728f8f"},
		"pyproject.toml":                                                                                {227, "3076234a966a3bdbb3a0d775576764709632e2e160594040b1fee65d8ad591bd"},
		"uv.lock":                                                                                       {3162, "ad825e872092be26169a6706c0d9643e88875d877f24bffc5c1a3471d82b1fb7"},
	}
	for name, want := range expected {
		t.Run(name, func(t *testing.T) {
			contents, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
			if err != nil {
				t.Fatal(err)
			}
			if want.size >= 0 && int64(len(contents)) != want.size {
				t.Fatalf("artifact %s size = %d, want %d", name, len(contents), want.size)
			}
			if got := fmt.Sprintf("%x", sha256.Sum256(contents)); got != want.sha256 {
				t.Fatalf("artifact %s checksum = %s, want %s", name, got, want.sha256)
			}
		})
	}
}
