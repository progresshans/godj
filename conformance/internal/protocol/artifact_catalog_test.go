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
// GDJ-0085 updates only the reviewed MIG-107 decision, its manifest/deviation
// entries and enclosing checksum list; all Django-owned observations stay fixed.
// GDJ-0100 separately adopts the reviewed SYS-028 ADR-0076 identity-adoption
// decision and SYS-023 credential transport bounds (1,024 username bytes,
// 2,060 frame bytes). No Django-owned observation changes with those decisions.
// GDJ-0103 keeps Pillow 12.3.0 in its own locked image reference project; the
// existing Django/DRF profile locks and oracle provenance remain unchanged.
// GDJ-0105 updates only read/Admin/API observations for the shared Article slug.
// Their bytes were reproduced independently under the pinned native profiles;
// the template/form observation and dependency/profile locks remain unchanged.
// GDJ-0117 normalizes only the Go-native API-011 route membership order.
// Both profile lists reproduce the independent pinned reference; all other
// observations and profile locks remain unchanged.
// GDJ-0118 adds the Article bulk route to both API-011 profiles and increments
// shared_routes; the independent oracle and all-scenario digest own this delta.
// GDJ-0138 adopts only the independently generated MIG-107 primary-key
// replacement refusal; forward non-PK removal is now a supported operation.
func TestReferenceArtifactsMatchLockedBytes(t *testing.T) {
	t.Parallel()
	root := conformanceRepositoryRoot(t)
	expected := map[string]struct {
		size   int64
		sha256 string
	}{
		"conformance/contracts/migration-target-plan-manifest.json":                                     {6796, "0636eb512d7de824b79d44d17373b3db4c2a6e6f7c712cc9e803480b33ce0496"},
		"conformance/fixtures/godj-migration-target-plan-not-implemented.json":                          {1707, "93bfc57abf15b2b2f2d713cf754bebf88f0c3db0a34fe880d7eec1bfc7c17e04"},
		"conformance/fixtures/godj-migration-target-plan-deviation-expected.json":                       {2673, "7e0c04e21237da15ab979d9b4bfec41cf81063c37e7ba5dd753c2dc0bfceb317"},
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-target-plan-oracle.json":          {43516, "0c104b05d7882dc0d740b011b8a4e63de7cb22af6a9b0cce047cd380f2e37473"},
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
		"conformance/contracts/migration-writer-manifest.json":                                          {9241, "2438140878186d891c42e85c6d6f3603c4554cd4787be7531afde0d746f12ff7"},
		"conformance/contracts/parameter-routing-manifest.json":                                         {4689, "85365b3670df5fa5a0d51241dd958d25816d9a285a5070e416120423793a264e"},
		"conformance/contracts/query-breadth-manifest.json":                                             {11282, "04665808db8f775096c07ac1705e6e10f139ac233f71a10c2892403005245167"},
		"conformance/contracts/query-cache-manifest.json":                                               {-1, "35f808e361d85228fe3048ae2510cf296f3127bee5572ce3ed9e66c6fd3eb3e2"},
		"conformance/contracts/query-expression-manifest.json":                                          {16592, "a32365e72bff2f96d576dc2a6322c703c6f0cf7c277776f6b326eda47cf9de17"},
		"conformance/contracts/relation-manifest.json":                                                  {10770, "791408c2c31864217f63b15218740214e4a850997d1e2b65dbb32b41586ff25b"},
		"conformance/contracts/save-lifecycle-manifest.json":                                            {-1, "6f215f6aee153954dee84d0571cc28529c2d50ee31ee2b9755733db3f9762905"},
		"conformance/contracts/system-state-manifest.json":                                              {16508, "8e23300b1b5f1ffad09d987339a9506ce192d16a6ca21a1f0b927d0f3e0c6e1a"},
		"conformance/contracts/template-form-manifest.json":                                             {7584, "4bc189cf71976b6d2ca301a97c6dfc5ee33463dec936f6c13d4181d56e1b1a41"},
		"conformance/contracts/write-migration-manifest.json":                                           {-1, "b0ba235cb8b83e9b595b2ad3230ea7440d8b6ea74789de27c8a1f6625ecd05bb"},
		"conformance/fixtures/godj-api-authentication-deviation-expected.json":                          {2291, "85a9a8b2261e7265b00a33c2cf5b63b9e5b5cd963b2ac7e894dd77988206fc4b"},
		"conformance/fixtures/godj-api-authentication-not-implemented.json":                             {1746, "c7bfcdb738377583b2e153c11e851fd97428b743d15d0b6e68c03577b25a643e"},
		"conformance/fixtures/godj-article-admin-deviation-expected.json":                               {915, "47d6f144259fbf82046d1e2821b2bd374c245cda30c084b627c5418500b54fc3"},
		"conformance/fixtures/godj-article-admin-not-implemented.json":                                  {1699, "bced1851b5e28e5001c890a7f94633b4f7068852e0bf424a148ffab8ca909480"},
		"conformance/fixtures/godj-article-api-deviation-expected.json":                                 {2003, "54758fdf850d4a61f65b764131a444c276ad7bb311a65d86b8b4a1780c979623"},
		"conformance/fixtures/godj-article-api-not-implemented.json":                                    {1736, "a8cbc212e7b49f0cfc91a61147bb3e585ff9c34594bebfdc3ed4dd26c53a7ea2"},
		"conformance/fixtures/godj-auth-session-deviation-expected.json":                                {1184, "f494fb64ae084d40564eaddad7e30792d5ed2f23799c8670e2a150ddb31a665d"},
		"conformance/fixtures/godj-auth-session-not-implemented.json":                                   {1553, "5e43fa9f0b4a18aae98a6f81f28ac678e6f8d5c69a8b360af3f59539a888ffc7"},
		"conformance/fixtures/godj-migration-command-not-implemented.json":                              {1838, "ff735a014fdf401de89e43c0a7ed9c6d02e2315e4347f858ca6165e5c670d511"},
		"conformance/fixtures/godj-migration-definition-source-not-implemented.json":                    {1574, "7356df3a0507a4ec737baeb6389213e949b006d0b7cf3bbd5cfb25ffb491d1cb"},
		"conformance/fixtures/godj-migration-execution-deviation-expected.json":                         {-1, "568495ed3dc5e6f3760c28f1c61c40dc54a63483c5b9c11283bf7ae5a8ac7547"},
		"conformance/fixtures/godj-migration-execution-not-implemented.json":                            {1685, "2d546cb84b16344aac13adbbe7eeddbfcf2a0d83d05513a857ba920cb7a6fd4b"},
		"conformance/fixtures/godj-migration-lifecycle-deviation-expected.json":                         {-1, "58e773ac6a2eb52faa6ecec78982e75219c5b978ae8295a8902e8bebe8158f1b"},
		"conformance/fixtures/godj-migration-lifecycle-not-implemented.json":                            {1681, "4d7e5f642372ede10b81680177c08edbea7a6fdc98812074789006c50d62260c"},
		"conformance/fixtures/godj-migration-planning-not-implemented.json":                             {1869, "a29a993b7774390e1d1253f8020aad14b436f567d1e15bdd84953b64012c4752"},
		"conformance/fixtures/godj-migration-project-check-not-implemented.json":                        {1729, "2880c34b7905690d1ef49bce3e314db6c95ef496d81e32d3780f5213973bf702"},
		"conformance/fixtures/godj-migration-relation-not-implemented.json":                             {1846, "9b2bc4f4c2a36fbf06c5b46b93e097efad573171b91de82ba95d540f5031fef4"},
		"conformance/fixtures/godj-migration-restart-not-implemented.json":                              {1715, "aeb65988819954f55bb3f6e3e523cd36f8f0cc516fa607e46e012e23e7b82540"},
		"conformance/fixtures/godj-migration-state-reconstruction-not-implemented.json":                 {1715, "ac757024e8d7258451814873847fb3818b54235d4503e01f885461f0839af634"},
		"conformance/fixtures/godj-migration-status-not-implemented.json":                               {1566, "9dc71f1e0c33bfa5e621f0ff2e0930cc891d8ec8736e39e530f137aff2b191cd"},
		"conformance/fixtures/godj-migration-writer-deviation-expected.json":                            {7247, "f0d8f5f4b0ad9ee56d49808efbfcc337f8e848a0603c0260680d020b2e0a8fa3"},
		"conformance/fixtures/godj-migration-writer-not-implemented.json":                               {1876, "5b0b140e5b3b1f8533e8a1afed32c8e4b3d5a75e47942b188e54249d252e2fd8"},
		"conformance/fixtures/godj-not-implemented.json":                                                {1794, "f089733688ffdf37a969ee1ee78dc6a9bce269a78f1e3434925b539f8f4d3134"},
		"conformance/fixtures/godj-parameter-routing-deviation-expected.json":                           {2174, "f9d084d178cccdf5928830813810f4d28c930d4414c73091a06dcd825ed38f60"},
		"conformance/fixtures/godj-parameter-routing-not-implemented.json":                              {1608, "4232dbcc81526aea4fb9b0e514c8590f4f5a09e959be8409dfe957cab6731364"},
		"conformance/fixtures/godj-query-breadth-not-implemented.json":                                  {1867, "7081aa0738d790d8e283bb915a5e9c920a932eecfa43cc9b30146065ca6e2891"},
		"conformance/fixtures/godj-query-cache-not-implemented.json":                                    {1790, "125b647943fd454c5b5c97d6dae0e1f84771261b22e70bda64180de1ba09950b"},
		"conformance/fixtures/godj-query-expression-not-implemented.json":                               {2465, "54947adfe0044734d872a2f59df2f1d36cfa060931d60e2e94e103cb8a0006e1"},
		"conformance/fixtures/godj-relation-not-implemented.json":                                       {1859, "0fd94b3e089d1b4040af719c926aa29ec58e343234b7a24edb65fb342aefc20f"},
		"conformance/fixtures/godj-save-lifecycle-not-implemented.json":                                 {1843, "c02aa06e856a3098bbd90679b1e21f87f5e418c6809ff1943f2e9dd125eeaa4f"},
		"conformance/fixtures/godj-system-state-deviation-expected.json":                                {1141, "a2877ae785b937b2b1c9ee3b567a7631403a5b5ca91485d2a6c942066c744869"},
		"conformance/fixtures/godj-system-state-not-implemented.json":                                   {3167, "0773e0a9c7ddd2998fc3802aa2e29a080da9405267cb995f2338363376fd71c2"},
		"conformance/fixtures/godj-template-form-deviation-expected.json":                               {1472, "0f9b10539677c07aa18c058f0e78925b3388299be853506324325c2af11d2ff3"},
		"conformance/fixtures/godj-template-form-not-implemented.json":                                  {1863, "162e186b50545d09e358afffcc0b1d5a8215b1fe8b79e23c2229819755362cea"},
		"conformance/fixtures/godj-write-migration-not-implemented.json":                                {1750, "e35270a6234457bd5e63266e24ad5c17d711bdb05187b94211032ad2100a9242"},
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/SHA256SUMS":                                 {2279, "ea1614f7c561a89f5b5a930c07b0b59ebdf75e0ed45fbac4903bc0f51e33726f"},
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/article-admin-oracle.json":                  {17711, "e718f31177bfa81b4cfe810f7cc13633b5d3465094be33e412c5ba6c3c2a6c2e"},
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/auth-session-oracle.json":                   {6916, "90f8fe223bde62cc3959945cfbf00fd0df02a5d89da4026f28372e703c819d68"},
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-command-oracle.json":              {12690, "0c950a5d6a6326351abaa43f950e5a8a7859fcb3771af1ee06d2ebb9bc2988f6"},
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-definition-source-oracle.json":    {29654, "23bb8914f1f2c0d6af519458738c528e603de1d5017ac460a5afeee0b5d4be8c"},
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-execution-oracle.json":            {47119, "9fc8c46d8750b0b9ad11db34514075e34aadfa0adc84bd77454426fe9c5239bd"},
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-lifecycle-oracle.json":            {98436, "dd039a56ee21abf873a3bd2a5f92861bea5b0160bfa742fd47d6c2017a39f53e"},
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-planning-oracle.json":             {39139, "9586075f25edca8eb82ab553c09416060d992978f0282d730c9d4232178c6938"},
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-project-check-oracle.json":        {19971, "a3c8040e2b80d9a8cbea60ce9fc442057f88f9fcb4b08519132261683dcf6588"},
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-relation-oracle.json":             {120502, "8483a93b506b421cac7ad54eb0ffcfd986b4374d22bf267f2f25cc3492e15ebf"},
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-restart-oracle.json":              {33888, "430349b6cb9c337d00b97b6fec59a46514bf0a41ae4db414a284a03c794f776f"},
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-state-reconstruction-oracle.json": {89997, "7c9ba85081bf5680882c2f4e0ac68aa926b5339a4305603a46d86a9a8caef40a"},
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-status-oracle.json":               {39478, "38e121058b00b6d28303d91bbf9f0d0a5efcd434c7c0dec6bf0e7d9d462f7d20"},
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/migration-writer-oracle.json":               {26015, "26ff5059c55db565b0babd5f4e62f16d513a0fa8f9f3d04273572b184661b5d1"},
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/oracle.json":                                {20529, "ba99ad6b7c1ccf68c92e272caf717d56c6aec43c0ef0f8ee4903e85d2a6b74fd"},
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/query-breadth-oracle.json":                  {41943, "b1b4a981a83df9705f60e2570847353f3f216e30dedc899b995cac0b76968141"},
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/query-cache-oracle.json":                    {56426, "8887677d4b89cae722313d90542b0916b364bccf9c9031261ecbd71c12978104"},
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/query-expression-oracle.json":               {87852, "463b2b0619b6e0d19030285b2fe268877aec0693ec35ffdfb877adc4dabc634c"},
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/relation-oracle.json":                       {33792, "ed5b7b008ecbdd8c2a67c8e13a8503170285aedab33f144f860d32a1c665a821"},
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/save-lifecycle-oracle.json":                 {11949, "3d2327edc32f83f740f9dd8b5e2f64b7887ad10f07a36e51ca860213be10a4b8"},
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/system-state.json":                          {38001, "fb9370af22749c949e85b5929772144ecd03890653b76e440c56b0e459503959"},
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/template-form-oracle.json":                  {12873, "ec3fe1dc73f919387b718dd3ee0ffea3b3a2ab7215dda5827a579109b9c2a8ab"},
		"conformance/oracles/django-6.1-sqlite-darwin-arm64/write-migration-oracle.json":                {15159, "04f398468251850059f85aa0550d03cd91d001056a33c2ad41e2d85361ba176f"},
		"conformance/oracles/drf-3.18.0-django-6.1-sqlite-darwin-arm64/SHA256SUMS":                      {283, "eab03b938c7990b812da1bea8bc4324aa5d9b46970569cd71915caadca6e8d0a"},
		"conformance/oracles/drf-3.18.0-django-6.1-sqlite-darwin-arm64/api-authentication-oracle.json":  {23804, "0bb0bd5d67a4ffa1fc9f8e71a9842ed8cc433a424d49104e5d65ef4b3009f51d"},
		"conformance/oracles/drf-3.18.0-django-6.1-sqlite-darwin-arm64/article-api-oracle.json":         {47099, "74242203be15f0ed0c1ddf685ae3ccf84775cd881b21c9beea175f01cc6f1f88"},
		"conformance/oracles/drf-3.18.0-django-6.1-sqlite-darwin-arm64/parameter-routing-oracle.json":   {12663, "66c17291b30f3dfe915ad39fd0d9cbaaa2082857b69637ad4dd6e6a7a7efdb9f"},
		"conformance/profiles/django-6.1-sqlite-darwin-arm64.json":                                      {879, "c5e56cfa5247c1d730331ad548b8e646b853cf9985fc61c195fd86b5057d7995"},
		"conformance/profiles/drf-3.18.0-django-6.1-sqlite-darwin-arm64.json":                           {916, "391d8a968ee2a1d9ebcd7a7aee809011274a858a8c61c4023690517015e84c0b"},
		"conformance/reference/drf/pyproject.toml":                                                      {320, "a897c3e9a7e1fa88705091e17a1f7726787d8f683ce6c2bcdc3683e41ee9f0b8"},
		"conformance/reference/drf/uv.lock":                                                             {4200, "d80b60923248d97d03689f9148364a37b368e0b844f75b41ca0f4eaaa5153472"},
		"conformance/reference/images/pyproject.toml":                                                   {256, "a0a954305f07f4e8c7aba8557651ce81f322b0d2d4712d23e5d2b37c495ead64"},
		"conformance/reference/images/uv.lock":                                                          {10313, "bfeca4743209ce9f11132104f0abe48634281f97a3570179ee68d9c0d449b2e9"},
		"pyproject.toml":                                                                                {228, "1f26a5ae351470c60f0cdb97580f817b8f20499a7ad0ee5e84e96722b954f570"},
		"uv.lock":                                                                                       {3163, "a91621c0cf2286a888302c606602a3facc6f4ed6c9856bb5eaad62af7e6b4f15"},
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

// Byte locks alone cannot detect a reviewed artifact update that forgot the
// profile naming that lock. Check the linkage on every platform, before the
// exact Darwin runner reaches environment-dependent oracle replay.
func TestReferenceProfileLocksMatchTheirEnvironmentFiles(t *testing.T) {
	t.Parallel()
	root := conformanceRepositoryRoot(t)
	for _, name := range []string{"django-6.1-sqlite-darwin-arm64", "drf-3.18.0-django-6.1-sqlite-darwin-arm64"} {
		t.Run(name, func(t *testing.T) {
			profile, err := LoadProfile(filepath.Join(root, "conformance", "profiles", name+".json"))
			if err != nil {
				t.Fatal(err)
			}
			contents, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(profile.Lock.File)))
			if err != nil {
				t.Fatal(err)
			}
			if got := fmt.Sprintf("%x", sha256.Sum256(contents)); got != profile.Lock.SHA256 {
				t.Fatalf("environment lock does not match profile provenance: got %s, want %s", got, profile.Lock.SHA256)
			}
		})
	}
}
