package get

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/linkerd/linkerd2/testutil"
)

//////////////////////
///   TEST SETUP   ///
//////////////////////

var TestHelper *testutil.TestHelper

func TestMain(m *testing.M) {
	TestHelper = testutil.NewTestHelper()
	os.Exit(m.Run())
}

type rowStat struct {
	name               string
	status             string
	meshed             string
	success            string
	rps                string
	p50Latency         string
	p95Latency         string
	p99Latency         string
	tcpOpenConnections string
}

//////////////////////
/// TEST EXECUTION ///
//////////////////////

// These tests retry for up to 20 seconds, since each call to "linkerd stat"
// generates traffic to the components in the linkerd namespace, and we're
// testing that those components are properly reporting stats. It's ok if the
// first few attempts fail due to missing stats, since the requests from those
// failed attempts will eventually be recorded in the stats that we're
// requesting, and the test will pass.
func TestCliStatForLinkerdNamespace(t *testing.T) {

	pods, err := TestHelper.GetPodNamesForDeployment(TestHelper.GetLinkerdNamespace(), "linkerd-prometheus")
	if err != nil {
		t.Fatalf("Failed to get pods for prometheus: %s", err)
	}
	if len(pods) != 1 {
		t.Fatalf("Expected 1 pod for prometheus, got %d", len(pods))
	}
	prometheusPod := pods[0]

	pods, err = TestHelper.GetPodNamesForDeployment(TestHelper.GetLinkerdNamespace(), "linkerd-controller")
	if err != nil {
		t.Fatalf("Failed to get pods for controller: %s", err)
	}
	if len(pods) != 1 {
		t.Fatalf("Expected 1 pod for controller, got %d", len(pods))
	}
	controllerPod := pods[0]

	prometheusAuthority := "linkerd-prometheus." + TestHelper.GetLinkerdNamespace() + ".svc.cluster.local:9090"

	for _, tt := range []struct {
		args         []string
		expectedRows map[string]string
		status       string
	}{
		{
			args: []string{"stat", "deploy", "-n", TestHelper.GetLinkerdNamespace()},
			expectedRows: map[string]string{
				"linkerd-controller":     "1/1",
				"linkerd-grafana":        "1/1",
				"linkerd-identity":       "1/1",
				"linkerd-prometheus":     "1/1",
				"linkerd-proxy-injector": "1/1",
				"linkerd-sp-validator":   "1/1",
				"linkerd-tap":            "1/1",
				"linkerd-web":            "1/1",
			},
		},
		{
			args: []string{"stat", fmt.Sprintf("po/%s", prometheusPod), "-n", TestHelper.GetLinkerdNamespace(), "--from", fmt.Sprintf("po/%s", controllerPod)},
			expectedRows: map[string]string{
				prometheusPod: "1/1",
			},
			status: "Running",
		},
		{
			args: []string{"stat", "deploy", "-n", TestHelper.GetLinkerdNamespace(), "--to", fmt.Sprintf("po/%s", prometheusPod)},
			expectedRows: map[string]string{
				"linkerd-controller": "1/1",
			},
		},
		{
			args: []string{"stat", "svc", "linkerd-prometheus", "-n", TestHelper.GetLinkerdNamespace(), "--from", "deploy/linkerd-controller"},
			expectedRows: map[string]string{
				"linkerd-prometheus": "1/1",
			},
		},
		{
			args: []string{"stat", "deploy", "-n", TestHelper.GetLinkerdNamespace(), "--to", "svc/linkerd-prometheus"},
			expectedRows: map[string]string{
				"linkerd-controller": "1/1",
			},
		},
		{
			args: []string{"stat", "ns", TestHelper.GetLinkerdNamespace()},
			expectedRows: map[string]string{
				TestHelper.GetLinkerdNamespace(): "8/8",
			},
		},
		{
			args: []string{"stat", "po", "-n", TestHelper.GetLinkerdNamespace(), "--to", fmt.Sprintf("au/%s", prometheusAuthority)},
			expectedRows: map[string]string{
				controllerPod: "1/1",
			},
			status: "Running",
		},
		{
			args: []string{"stat", "au", "-n", TestHelper.GetLinkerdNamespace(), "--to", fmt.Sprintf("po/%s", prometheusPod)},
			expectedRows: map[string]string{
				prometheusAuthority: "-",
			},
		},
	} {
		tt := tt // pin
		t.Run("linkerd "+strings.Join(tt.args, " "), func(t *testing.T) {
			err := TestHelper.RetryFor(20*time.Second, func() error {
				out, stderr, err := TestHelper.LinkerdRun(tt.args...)
				if err != nil {
					t.Fatalf("Unexpected stat error: %s\n%s", err, out)
				}
				fmt.Println(stderr)

				expectedColumnCount := 8
				if tt.status != "" {
					expectedColumnCount++
				}
				rowStats, err := parseRows(out, len(tt.expectedRows), expectedColumnCount)
				if err != nil {
					return err
				}

				for name, meshed := range tt.expectedRows {
					if err := validateRowStats(name, meshed, tt.status, rowStats); err != nil {
						return err
					}
				}

				return nil
			})
			if err != nil {
				t.Fatal(err.Error())
			}
		})
	}
}

// check that expectedRowCount rows have been returned
func checkRowCount(out string, expectedRowCount int) ([]string, error) {
	rows := strings.Split(out, "\n")
	if len(rows) < 2 {
		return nil, fmt.Errorf(
			"Error stripping header and trailing newline; full output:\n%s",
			strings.Join(rows, "\n"),
		)
	}
	rows = rows[1 : len(rows)-1] // strip header and trailing newline

	if len(rows) != expectedRowCount {
		return nil, fmt.Errorf(
			"Expected [%d] rows in stat output, got [%d]; full output:\n%s",
			expectedRowCount, len(rows), strings.Join(rows, "\n"))
	}

	return rows, nil
}

func parseRows(out string, expectedRowCount, expectedColumnCount int) (map[string]*rowStat, error) {
	rows, err := checkRowCount(out, expectedRowCount)
	if err != nil {
		return nil, err
	}

	rowStats := make(map[string]*rowStat)
	for _, row := range rows {
		fields := strings.Fields(row)

		if expectedColumnCount == 0 {
			expectedColumnCount = 8
		}
		if len(fields) != expectedColumnCount {
			return nil, fmt.Errorf(
				"Expected [%d] columns in stat output, got [%d]; full output:\n%s",
				expectedColumnCount, len(fields), row)
		}

		rowStats[fields[0]] = &rowStat{
			name: fields[0],
		}

		i := 0
		if expectedColumnCount == 9 {
			rowStats[fields[0]].status = fields[1]
			i = 1
		}
		rowStats[fields[0]].meshed = fields[1+i]
		rowStats[fields[0]].success = fields[2+i]
		rowStats[fields[0]].rps = fields[3+i]
		rowStats[fields[0]].p50Latency = fields[4+i]
		rowStats[fields[0]].p95Latency = fields[5+i]
		rowStats[fields[0]].p99Latency = fields[6+i]
		rowStats[fields[0]].tcpOpenConnections = fields[7+i]
	}

	return rowStats, nil
}

func validateRowStats(name, expectedMeshCount, expectedStatus string, rowStats map[string]*rowStat) error {
	stat, ok := rowStats[name]
	if !ok {
		return fmt.Errorf("No stats found for [%s]", name)
	}

	if stat.status != expectedStatus {
		return fmt.Errorf("Expected status '%s' for '%s', got '%s'",
			expectedStatus, name, stat.status)
	}

	if stat.meshed != expectedMeshCount {
		return fmt.Errorf("Expected mesh count [%s] for [%s], got [%s]",
			expectedMeshCount, name, stat.meshed)
	}

	expectedSuccessRate := "100.00%"
	if stat.success != expectedSuccessRate {
		return fmt.Errorf("Expected success rate [%s] for [%s], got [%s]",
			expectedSuccessRate, name, stat.success)
	}

	if !strings.HasSuffix(stat.rps, "rps") {
		return fmt.Errorf("Unexpected rps for [%s], got [%s]",
			name, stat.rps)
	}

	if !strings.HasSuffix(stat.p50Latency, "ms") {
		return fmt.Errorf("Unexpected p50 latency for [%s], got [%s]",
			name, stat.p50Latency)
	}

	if !strings.HasSuffix(stat.p95Latency, "ms") {
		return fmt.Errorf("Unexpected p95 latency for [%s], got [%s]",
			name, stat.p95Latency)
	}

	if !strings.HasSuffix(stat.p99Latency, "ms") {
		return fmt.Errorf("Unexpected p99 latency for [%s], got [%s]",
			name, stat.p99Latency)
	}

	if stat.tcpOpenConnections != "-" {
		_, err := strconv.Atoi(stat.tcpOpenConnections)
		if err != nil {
			return fmt.Errorf("Error parsing number of TCP connections [%s]: %s", stat.tcpOpenConnections, err.Error())
		}
	}

	return nil
}
