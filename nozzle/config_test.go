package splunknozzle_test

import (
	"fmt"
	"os"
	"time"

	"github.com/cloudfoundry-community/splunk-firehose-nozzle/drain"
	. "github.com/cloudfoundry-community/splunk-firehose-nozzle/nozzle"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

var _ = Describe("Config", func() {

	var (
		version        = "1.0"
		branch         = "develop"
		commit         = "08a9e9bd557ca9038e9b391d9a77d47aa56210a3"
		buildos        = "Linux"
		mapping        = `{"default_index":"otherindex","mappings":[{"by":"cf_org_id","value":"cf_org_id_test","index":"cf_org_id_idx"},{"by":"cf_space_id","value":"cf_space_id_test","index":"cf_space_id_idx"},{"by": "cf_app_id","value":"cf_app_id_test","index":"cf_app_id_idx"}]}`
		invalidMapping = `{"default_index":"main","mappings":[{"by":"cf_org_id","value": "testing", "index": null}]}}`
	)

	verifyIndexMapping := func(indexMapping *drain.IndexMapConfig) {
		Expect(indexMapping).ShouldNot(Equal(nil))
		Expect(indexMapping.DefaultIndex).To(Equal("otherindex"))
		Expect(indexMapping.Mappings).ShouldNot(Equal(nil))
		Expect(len(indexMapping.Mappings)).To(Equal(3))

		byIDs := []string{drain.CF_ORG_ID, drain.CF_SPACE_ID, drain.CF_APP_ID}
		for i, idxMap := range indexMapping.Mappings {
			Expect(idxMap.By).To(Equal(byIDs[i]))
			Expect(idxMap.Value).To(Equal(byIDs[i] + "_test"))
			Expect(*idxMap.Index).To(Equal(byIDs[i] + "_idx"))
		}
	}

	Context("Env config parsing", func() {

		BeforeEach(func() {
			// FIX "nozzle.test: error: unknown short flag '-t', try --help" error when coverage
			os.Args = os.Args[:1]
			os.Clearenv()

			os.Setenv("API_ENDPOINT", "api.bosh-lite.com")
			os.Setenv("API_USER", "admin")
			os.Setenv("API_PASSWORD", "abc123")
			os.Setenv("SPLUNK_TOKEN", "sometoken")
			os.Setenv("SPLUNK_HOST", "splunk.example.com")
			os.Setenv("SPLUNK_INDEX_MAPPING", mapping)
		})

		It("parses config from environment", func() {
			os.Setenv("DEBUG", "true")
			os.Setenv("SKIP_SSL_VALIDATION", "true")
			os.Setenv("JOB_NAME", "my-job")
			os.Setenv("JOB_INDEX", "2")
			os.Setenv("JOB_HOST", "nozzle.example.com")

			os.Setenv("ADD_APP_INFO", "true")
			os.Setenv("BOLTDB_PATH", "foo.db")

			os.Setenv("EVENTS", "LogMessage")
			os.Setenv("EXTRA_FIELDS", "foo:bar")

			os.Setenv("FIREHOSE_KEEP_ALIVE", "42s")
			os.Setenv("FIREHOSE_SUBSCRIPTION_ID", "my-nozzle")

			os.Setenv("FLUSH_INTERVAL", "43s")

			c, err := NewConfigFromCmdFlags(version, branch, commit, buildos)
			Ω(err).ShouldNot(HaveOccurred())

			Expect(c.Debug).To(BeTrue())
			Expect(c.SkipSSL).To(BeTrue())
			Expect(c.JobName).To(Equal("my-job"))
			Expect(c.JobIndex).To(Equal("2"))
			Expect(c.JobHost).To(Equal("nozzle.example.com"))

			Expect(c.AddAppInfo).To(BeTrue())
			Expect(c.ApiEndpoint).To(Equal("api.bosh-lite.com"))
			Expect(c.User).To(Equal("admin"))
			Expect(c.Password).To(Equal("abc123"))
			Expect(c.BoltDBPath).To(Equal("foo.db"))

			Expect(c.WantedEvents).To(Equal("LogMessage"))
			Expect(c.ExtraFields["foo"]).To(Equal("bar"))

			Expect(c.KeepAlive).To(Equal(42 * time.Second))
			Expect(c.SubscriptionID).To(Equal("my-nozzle"))

			Expect(c.SplunkToken).To(Equal("sometoken"))
			Expect(c.SplunkHost).To(Equal("splunk.example.com"))
			verifyIndexMapping(c.IndexMapping)
			Expect(c.FlushInterval).To(Equal(43 * time.Second))
			Expect(c.Version).To(Equal(version))
			Expect(c.Branch).To(Equal(branch))
			Expect(c.Commit).To(Equal(commit))
			Expect(c.BuildOS).To(Equal(buildos))
		})

		It("check defaults", func() {
			c, err := NewConfigFromCmdFlags(version, branch, commit, buildos)
			Ω(err).ShouldNot(HaveOccurred())

			Expect(c.Debug).To(BeFalse())
			Expect(c.SkipSSL).To(BeFalse())
			Expect(c.JobName).To(Equal("splunk-nozzle"))
			Expect(c.JobIndex).To(Equal("-1"))
			Expect(c.JobHost).To(Equal(""))

			// Since we have mapping, so AddAppInfo will be reset to true
			Expect(c.AddAppInfo).To(Equal(true))
			Expect(c.BoltDBPath).To(Equal("cache.db"))

			Expect(c.WantedEvents).To(Equal("ValueMetric,CounterEvent,ContainerMetric"))

			Expect(c.KeepAlive).To(Equal(25 * time.Second))
			Expect(c.SubscriptionID).To(Equal("splunk-firehose"))

			Expect(c.FlushInterval).To(Equal(5 * time.Second))
			Expect(c.QueueSize).To(Equal(10000))
			Expect(c.BatchSize).To(Equal(1000))
			Expect(c.Retries).To(Equal(5))
			Expect(c.HecWorkers).To(Equal(8))
		})

		It("Invalid index mapping with invliad json", func() {
			os.Setenv("SPLUNK_INDEX_MAPPING", invalidMapping)
			_, err := NewConfigFromCmdFlags(version, branch, commit, buildos)
			Ω(err).Should(HaveOccurred())
		})

		It("Invalid extra fields", func() {
			os.Setenv("EXTRA_FIELDS", "abc")
			_, err := NewConfigFromCmdFlags(version, branch, commit, buildos)
			Ω(err).Should(HaveOccurred())
		})
	})

	Context("Flags config parsing", func() {
		BeforeEach(func() {
			os.Clearenv()
			// FIX "nozzle.test: error: unknown short flag '-t', try --help" error when coverage
			args := []string{
				"splunk-firehose-nozzle",
				"--api-endpoint=api.bosh-lite.comc",
				"--user=adminc",
				"--password=abc123c",
				"--splunk-token=sometokenc",
				"--splunk-host=splunk.example.comc",
				fmt.Sprintf("--splunk-index-mapping=%s", mapping),
				"--job-name=my-jobc",
				"--job-index=3",
				"--job-host=nozzle.example.comc",
				"--add-app-info",
				"--debug",
				"--skip-ssl-validation",
				"--boltdb-path=foo.dbc",
				"--events=LogMessagec",
				"--extra-fields=foo:barc",
				"--firehose-keep-alive=24s",
				"--subscription-id=my-nozzlec",
				"--flush-interval=34s",
			}
			os.Args = args
		})

		It("parses config from cli flags", func() {
			c, err := NewConfigFromCmdFlags(version, branch, commit, buildos)
			Ω(err).ShouldNot(HaveOccurred())

			Expect(c.ApiEndpoint).To(Equal("api.bosh-lite.comc"))
			Expect(c.User).To(Equal("adminc"))
			Expect(c.Password).To(Equal("abc123c"))
			Expect(c.SplunkToken).To(Equal("sometokenc"))
			Expect(c.SplunkHost).To(Equal("splunk.example.comc"))
			verifyIndexMapping(c.IndexMapping)

			Expect(c.JobName).To(Equal("my-jobc"))
			Expect(c.JobIndex).To(Equal("3"))
			Expect(c.JobHost).To(Equal("nozzle.example.comc"))

			Expect(c.Debug).To(BeTrue())
			Expect(c.AddAppInfo).To(BeTrue())
			Expect(c.SkipSSL).To(BeTrue())

			Expect(c.BoltDBPath).To(Equal("foo.dbc"))
			Expect(c.WantedEvents).To(Equal("LogMessagec"))

			Expect(c.ExtraFields["foo"]).To(Equal("barc"))
			Expect(c.KeepAlive).To(Equal(24 * time.Second))
			Expect(c.SubscriptionID).To(Equal("my-nozzlec"))
			Expect(c.FlushInterval).To(Equal(34 * time.Second))

			Expect(c.Version).To(Equal(version))
			Expect(c.Branch).To(Equal(branch))
			Expect(c.Commit).To(Equal(commit))
			Expect(c.BuildOS).To(Equal(buildos))
		})
	})
})
