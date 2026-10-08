//go:build e2e

/*
Copyright 2026 Peter Rozsa.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package e2e

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/rozsapeter96/impala-operator/test/utils"
)

const (
	clusterNamespace = "impala-e2e"
	clusterName      = "e2e"
	clientPod        = "impala-client"
	impalaVersion    = "4.5.2"
	// impalaMasterVersion tags prozsa/impala images built from Impala master.
	impalaMasterVersion = "baf1e8fe93"
	restClusterName     = "rest"
	coordinatorAddr     = clusterName + "-coordinator:21050"
	envTrue             = "true"
)

// impalaImages are pre-loaded into kind so that the test does not depend on
// pulling multi-gigabyte images inside the cluster.
var impalaImages = []string{
	"apache/impala:" + impalaVersion + "-statestored",
	// Iceberg REST catalog support is in Impala master only; these are
	// master builds (statestored, coordinator and executor; no catalogd runs
	// in that cluster).
	"prozsa/impala:" + impalaMasterVersion + "-statestored",
	"prozsa/impala:" + impalaMasterVersion + "-impalad_coordinator",
	"prozsa/impala:" + impalaMasterVersion + "-impalad_executor",
	"apache/polaris:1.8.0",
	"apache/impala:" + impalaVersion + "-catalogd",
	"apache/impala:" + impalaVersion + "-impalad_coordinator",
	"apache/impala:" + impalaVersion + "-impalad_executor",
	"apache/impala:" + impalaVersion + "-impala_quickstart_hms",
	"apache/impala:" + impalaVersion + "-impala_quickstart_client",
	"quay.io/minio/minio:RELEASE.2025-09-07T16-13-09Z",
	"quay.io/minio/mc:RELEASE.2025-08-13T08-35-41Z",
}

func kubectl(args ...string) (string, error) {
	return utils.Run(exec.Command("kubectl", append([]string{"-n", clusterNamespace}, args...)...))
}

// impalaShell runs a query through impala-shell in the client pod. Small
// queries are forced onto executors so that the tests exercise them instead
// of the coordinator's single-node fast path.
func impalaShell(query string) (string, error) {
	return impalaShellInPool("", query)
}

// impalaShellInPool is impalaShell with REQUEST_POOL set (empty = default pool).
func impalaShellInPool(pool, query string) (string, error) {
	return impalaShellAt(coordinatorAddr, pool, query)
}

// impalaShellAt runs a query against the given coordinator address.
func impalaShellAt(addr, pool, query string) (string, error) {
	args := []string{"exec", clientPod, "--", "impala-shell", "-i", addr, "-B", "--quiet",
		"--query_option=EXEC_SINGLE_NODE_ROWS_THRESHOLD=0"}
	if pool != "" {
		args = append(args, "--query_option=REQUEST_POOL="+pool)
	}
	return kubectl(append(args, "-q", query)...)
}

func statefulSetExists(name string) bool {
	_, err := kubectl("get", "statefulset", name)
	return err == nil
}

func clusterCondition(condType string) string {
	return clusterConditionOf(clusterName, condType)
}

func clusterConditionOf(name, condType string) string {
	out, err := kubectl("get", "impalacluster", name, "-o",
		fmt.Sprintf(`jsonpath={.status.conditions[?(@.type=="%s")].status}`, condType))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

var _ = Describe("ImpalaCluster", Ordered, func() {
	BeforeAll(func() {
		if os.Getenv("IMPALA_E2E_SKIP_IMAGE_LOAD") != envTrue {
			By("loading Impala images into kind")
			for _, img := range impalaImages {
				_, _ = utils.Run(exec.Command("docker", "pull", "-q", img))
				Expect(utils.LoadImageToKindClusterWithName(img)).To(Succeed(), img)
			}
		}

		// Top-level Ginkgo containers run in random order, so make sure the
		// operator is deployed regardless of whether the Manager suite ran first.
		By("deploying the operator")
		_, err := utils.Run(exec.Command("make", "install"))
		Expect(err).NotTo(HaveOccurred())
		_, err = utils.Run(exec.Command("make", "deploy", fmt.Sprintf("IMG=%s", managerImage)))
		Expect(err).NotTo(HaveOccurred())
		// The image tag is fixed, so a Deployment left over from an earlier run
		// would keep running the previous build. Restart it to pick up the
		// freshly loaded image.
		_, err = utils.Run(exec.Command("kubectl", "-n", namespace, "rollout", "restart",
			"deployment/impala-operator-controller-manager"))
		Expect(err).NotTo(HaveOccurred())
		_, err = utils.Run(exec.Command("kubectl", "-n", namespace, "rollout", "status",
			"deployment/impala-operator-controller-manager", "--timeout=5m"))
		Expect(err).NotTo(HaveOccurred())

		By("creating the test namespace with MinIO and a Hive Metastore")
		_, _ = utils.Run(exec.Command("kubectl", "create", "ns", clusterNamespace))
		_, err = kubectl("apply", "-f", "test/e2e/fixtures/minio.yaml")
		Expect(err).NotTo(HaveOccurred())
		_, err = kubectl("apply", "-f", "test/e2e/fixtures/hms.yaml")
		Expect(err).NotTo(HaveOccurred())
		_, err = kubectl("wait", "--for=condition=available", "deployment/minio", "deployment/hms", "--timeout=10m")
		Expect(err).NotTo(HaveOccurred())
		_, err = kubectl("wait", "--for=condition=complete", "job/minio-bucket", "--timeout=5m")
		Expect(err).NotTo(HaveOccurred())

		By("starting an impala-shell client pod")
		if _, err := kubectl("get", "pod", clientPod); err != nil {
			// Absent (or a kept namespace from an earlier IMPALA_E2E_KEEP run
			// already has one): only create it when missing.
			_, err = kubectl("run", clientPod, "--image=apache/impala:"+impalaVersion+"-impala_quickstart_client",
				"--image-pull-policy=IfNotPresent", "--restart=Never", "--command", "--", "sleep", "infinity")
			Expect(err).NotTo(HaveOccurred())
		}
		_, err = kubectl("wait", "--for=condition=ready", "pod/"+clientPod, "--timeout=5m")
		Expect(err).NotTo(HaveOccurred())
	})

	AfterAll(func() {
		if CurrentSpecReport().Failed() || os.Getenv("IMPALA_E2E_KEEP") == envTrue {
			By("dumping diagnostics")
			out, _ := kubectl("get", "impalacluster", clusterName, "-o", "yaml")
			_, _ = fmt.Fprintln(GinkgoWriter, out)
			out, _ = kubectl("get", "pods", "-o", "wide")
			_, _ = fmt.Fprintln(GinkgoWriter, out)
			out, _ = kubectl("get", "events", "--sort-by=.lastTimestamp")
			_, _ = fmt.Fprintln(GinkgoWriter, out)
			out, _ = utils.Run(exec.Command("kubectl", "-n", namespace, "logs",
				"deployment/impala-operator-controller-manager", "--tail=200"))
			_, _ = fmt.Fprintln(GinkgoWriter, out)
			for _, pod := range []string{clusterName + "-statestore-0", clusterName + "-catalog-0",
				clusterName + "-coordinator-0", clusterName + "-exec-small-0-0",
				restClusterName + "-coordinator-0", restClusterName + "-exec-small-0-0", "deployment/polaris", "job/polaris-setup"} {
				out, _ = kubectl("logs", pod, "--tail=60")
				_, _ = fmt.Fprintf(GinkgoWriter, "--- %s ---\n%s\n", pod, out)
			}
		}
		if os.Getenv("IMPALA_E2E_KEEP") != envTrue {
			_, _ = utils.Run(exec.Command("kubectl", "delete", "ns", clusterNamespace, "--wait=true"))
			_, _ = utils.Run(exec.Command("make", "undeploy", "ignore-not-found=true"))
		}
	})

	It("becomes ready", func() {
		_, err := kubectl("apply", "-f", "test/e2e/fixtures/impalacluster.yaml")
		Expect(err).NotTo(HaveOccurred())

		Eventually(func() string { return clusterCondition("Ready") }, 15*time.Minute, 10*time.Second).Should(Equal("True"))

		out, err := kubectl("get", "impalacluster", clusterName, "-o", "jsonpath={.status.executorGroups[0].groups[0].name}")
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(Equal("root.default-small-0"))
	})

	It("answers queries and stores tables on S3", func() {
		Eventually(func() error {
			_, err := impalaShell("select 1")
			return err
		}, 5*time.Minute, 10*time.Second).Should(Succeed())

		_, err := impalaShell("create database if not exists e2e")
		Expect(err).NotTo(HaveOccurred())
		_, err = impalaShell("create table if not exists e2e.t (id int) stored as parquet")
		Expect(err).NotTo(HaveOccurred())
		_, err = impalaShell("insert overwrite e2e.t values (1),(2),(3)")
		Expect(err).NotTo(HaveOccurred())

		out, err := impalaShell("select count(*) from e2e.t")
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.TrimSpace(out)).To(Equal("3"))

		out, err = impalaShell("show table stats e2e.t")
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("s3a://warehouse/"))
	})

	It("drains an executor gracefully when its pod is deleted", func() {
		// Roughly 15s of executor work: sleep(5000) is evaluated per row in
		// the scan (non-deterministic functions are not allowed in predicates).
		type result struct {
			out string
			err error
		}
		done := make(chan result, 1)
		go func() {
			defer GinkgoRecover()
			out, err := impalaShell("select count(sleep(5000)) from e2e.t")
			done <- result{out, err}
		}()
		time.Sleep(4 * time.Second)

		start := time.Now()
		_, err := kubectl("delete", "pod", clusterName+"-exec-small-0-0", "--wait=true")
		Expect(err).NotTo(HaveOccurred())
		deleteDuration := time.Since(start)

		res := <-done
		Expect(res.err).NotTo(HaveOccurred(), "query must survive the executor drain")
		Expect(strings.TrimSpace(res.out)).To(Equal("3"))
		Expect(deleteDuration).To(BeNumerically(">", 5*time.Second), "pod must wait for the running query")

		Eventually(func() string { return clusterCondition("Ready") }, 10*time.Minute, 10*time.Second).Should(Equal("True"))
	})

	It("scales an executor group family up on demand and back down when idle", func() {
		// The "burst" family starts with zero groups. Two concurrent queries
		// in its pool first queue with "Waiting for executors to start"
		// (cold start -> group 0), then, because each executor has a single
		// admission slot, the second query queues with "Not enough admission
		// control slots" (-> group 1). Both groups are removed once idle.
		// This is the positive counterpart of the pool-cap test below: it
		// proves the /admission scrape and the queue-reason gating work
		// against a real coordinator, not just the unit-test fixtures.
		Expect(statefulSetExists(clusterName+"-exec-burst-0")).To(BeFalse(), "burst family must start with no groups")

		type result struct {
			out string
			err error
		}
		results := make(chan result, 2)
		for range 2 {
			go func() {
				defer GinkgoRecover()
				out, err := impalaShellInPool("root.burst", "select count(sleep(20000)) from e2e.t")
				results <- result{out, err}
			}()
		}

		By("cold-starting the first group")
		Eventually(func() bool { return statefulSetExists(clusterName + "-exec-burst-0") },
			2*time.Minute, 5*time.Second).Should(BeTrue())
		By("adding a second group while the first is slot-saturated")
		Eventually(func() bool { return statefulSetExists(clusterName + "-exec-burst-1") },
			5*time.Minute, 5*time.Second).Should(BeTrue())

		for range 2 {
			res := <-results
			Expect(res.err).NotTo(HaveOccurred())
			Expect(strings.TrimSpace(res.out)).To(Equal("3"))
		}

		By("removing both groups once idle")
		Eventually(func() bool {
			return !statefulSetExists(clusterName+"-exec-burst-1") && !statefulSetExists(clusterName+"-exec-burst-0")
		}, 10*time.Minute, 10*time.Second).Should(BeTrue())
		Eventually(func() (string, error) {
			return kubectl("get", "impalacluster", clusterName, "-o",
				"jsonpath={.status.executorGroups[?(@.name==\"burst\")].desiredGroups}")
		}, 1*time.Minute, 5*time.Second).Should(Equal("0"))
	})

	It("does not add a group for a queue caused by the pool running-query cap", func() {
		// The fixture pool allows one running query (maxRunningQueries: 1), so a
		// second concurrent query queues with head_queued_reason "number of
		// running queries 1 is at or over limit 1". Adding an executor group
		// cannot drain that queue, so the autoscaler must leave the group count
		// alone. This is the regression guard for the admission-aware policy.
		type result struct{ err error }
		results := make(chan result, 2)
		for range 2 {
			go func() {
				defer GinkgoRecover()
				_, err := impalaShell("select count(sleep(20000)) from e2e.t")
				results <- result{err}
			}()
		}

		// Confirm a queue actually formed (the head reason is the pool cap).
		Eventually(func() (string, error) {
			return kubectl("get", "impalacluster", clusterName, "-o",
				"jsonpath={.status.executorGroups[0].desiredGroups}")
		}, 1*time.Minute, 5*time.Second).Should(Equal("1"))

		// Hold well past scaleUpDelaySeconds (10s in the fixture) and assert the
		// autoscaler never adds a second group.
		Consistently(func() error {
			if statefulSetExists(clusterName + "-exec-small-1") {
				return fmt.Errorf("autoscaler wrongly added a group for a pool-cap queue")
			}
			return nil
		}, 90*time.Second, 10*time.Second).Should(Succeed())

		for range 2 {
			Expect((<-results).err).NotTo(HaveOccurred())
		}
	})
	It("serves an Apache Polaris REST catalog without catalogd", func() {
		// The kind node cannot host two Impala clusters, so the HMS-backed one
		// makes room for a master-build cluster that has no catalogd and no
		// metastore: its coordinator reads the "lake" catalog from Polaris,
		// which stores metadata on the same MinIO bucket. The setup Job in
		// polaris.yaml creates an empty Iceberg table; Impala writes it with
		// INSERT INTO (routed by the catalog name) and reads it back.
		By("replacing the HMS cluster with Polaris and a standalone REST cluster")
		_, err := kubectl("delete", "impalacluster", clusterName, "--wait=true", "--ignore-not-found")
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() (string, error) {
			return kubectl("get", "pods", "-l", "app.kubernetes.io/instance="+clusterName, "-o", "name")
		}, 5*time.Minute, 5*time.Second).Should(BeEmpty())

		_, err = kubectl("apply", "-f", "test/e2e/fixtures/polaris.yaml")
		Expect(err).NotTo(HaveOccurred())
		_, err = kubectl("wait", "--for=condition=available", "deployment/polaris", "--timeout=10m")
		Expect(err).NotTo(HaveOccurred())
		_, err = kubectl("wait", "--for=condition=complete", "job/polaris-setup", "--timeout=5m")
		Expect(err).NotTo(HaveOccurred())

		_, err = kubectl("apply", "-f", "test/e2e/fixtures/impalacluster-rest.yaml")
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() string { return clusterConditionOf(restClusterName, "Ready") }, 15*time.Minute, 10*time.Second).Should(Equal("True"))
		Expect(statefulSetExists(restClusterName+"-catalog")).To(BeFalse(), "no catalogd without a Hive Metastore")

		By("writing and reading an Iceberg table through the REST catalog")
		addr := restClusterName + "-coordinator:21050"
		Eventually(func() error {
			_, err := impalaShellAt(addr, "", "select 1")
			return err
		}, 5*time.Minute, 10*time.Second).Should(Succeed())

		out, err := impalaShellAt(addr, "", "show tables in e2e_rest")
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("t"))

		_, err = impalaShellAt(addr, "", "insert into e2e_rest.t values (1),(2),(3)")
		Expect(err).NotTo(HaveOccurred())
		out, err = impalaShellAt(addr, "", "select count(*) from e2e_rest.t")
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.TrimSpace(out)).To(Equal("3"))
		out, err = impalaShellAt(addr, "", "describe formatted e2e_rest.t")
		Expect(err).NotTo(HaveOccurred())
		Expect(out).To(ContainSubstring("s3a://warehouse/lake/"))
	})
})
