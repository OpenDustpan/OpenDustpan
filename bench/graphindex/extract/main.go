// Command extractbench generates a synthetic OpenTofu state and measures the
// extraction of identity-shaped values: JSON decode, allowlist match, a scan
// of JSON strings for ARNs, and an HMAC-SHA-256 of each kept value.
package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
)

var (
	arnRE   = regexp.MustCompile(`^arn:aws(-cn|-us-gov)?:[a-z0-9-]+:[a-z0-9-]*:([0-9]{12})?:.+$`)
	shortRE = regexp.MustCompile(`^(vpc|subnet|sg|igw|nat|rtb|eni|i|vol|tgw|vpce|pcx|acl|eipalloc|lt)-([0-9a-f]{8}|[0-9a-f]{17})$`)
	arnIn   = regexp.MustCompile(`arn:aws(-cn|-us-gov)?:[a-z0-9-]+:[a-z0-9-]*:([0-9]{12})?:[A-Za-z0-9_/.:*+=,@-]{1,500}`)
)

// rng is splitmix64: a fixed sequence, so each run makes the same state.
type rng struct{ s uint64 }

func (r *rng) IntN(n int) int {
	r.s += 0x9e3779b97f4a7c15
	z := r.s
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	z ^= z >> 31
	return int(z % uint64(n))
}

func hexs(r *rng, n int) string {
	const h = "0123456789abcdef"
	b := make([]byte, n)
	for i := range b {
		b[i] = h[r.IntN(16)]
	}
	return string(b)
}

func gen(r *rng, n int) []byte {
	type inst struct {
		SchemaVersion int            `json:"schema_version"`
		Attributes    map[string]any `json:"attributes"`
		Sensitive     []any          `json:"sensitive_attributes"`
		Dependencies  []string       `json:"dependencies,omitempty"`
	}
	type res struct {
		Mode      string `json:"mode"`
		Type      string `json:"type"`
		Name      string `json:"name"`
		Provider  string `json:"provider"`
		Instances []inst `json:"instances"`
	}
	vpc := "vpc-" + hexs(r, 17)
	subnets := []string{"subnet-" + hexs(r, 17), "subnet-" + hexs(r, 17), "subnet-" + hexs(r, 17)}
	sg := "sg-" + hexs(r, 17)
	acct := "123456789012"
	resources := make([]res, 0, n)
	for i := 0; i < n; i++ {
		id := "i-" + hexs(r, 17)
		attrs := map[string]any{
			"id": id, "arn": "arn:aws:ec2:eu-west-1:" + acct + ":instance/" + id,
			"ami": "ami-" + hexs(r, 17), "instance_type": "m6i.large", "availability_zone": "eu-west-1a",
			"subnet_id": subnets[i%3], "vpc_security_group_ids": []string{sg},
			"iam_instance_profile": "app-" + fmt.Sprint(i),
			"tags":                 map[string]string{"Name": fmt.Sprintf("app-%d", i), "team": "payments", "env": "prod", "cost-center": "cc-1234"},
			"tags_all":             map[string]string{"Name": fmt.Sprintf("app-%d", i), "team": "payments", "env": "prod", "cost-center": "cc-1234", "managed-by": "opentofu"},
			"private_ip":           "10.0.1.23", "private_dns": "ip-10-0-1-23.eu-west-1.compute.internal",
			"root_block_device":    []any{map[string]any{"volume_id": "vol-" + hexs(r, 17), "volume_size": 50, "volume_type": "gp3", "encrypted": true, "kms_key_id": "arn:aws:kms:eu-west-1:" + acct + ":key/" + hexs(r, 8) + "-" + hexs(r, 4) + "-" + hexs(r, 4) + "-" + hexs(r, 4) + "-" + hexs(r, 12), "iops": 3000, "throughput": 125, "delete_on_termination": true, "tags": map[string]string{}}},
			"metadata_options":     []any{map[string]any{"http_endpoint": "enabled", "http_tokens": "required", "http_put_response_hop_limit": 2, "instance_metadata_tags": "disabled"}},
			"user_data":            strings.Repeat("IyEvYmluL2Jhc2gKZWNobyBoZWxsbwo=", 40),
			"policy":               fmt.Sprintf(`{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":["s3:GetObject"],"Resource":["arn:aws:s3:::bucket-%d/*"]},{"Effect":"Allow","Action":["sqs:SendMessage"],"Resource":"arn:aws:sqs:eu-west-1:%s:queue-%d"}]}`, i, acct, i),
			"network_interface":    []any{map[string]any{"network_interface_id": "eni-" + hexs(r, 17), "device_index": 0}},
			"credit_specification": []any{}, "ebs_block_device": []any{}, "ephemeral_block_device": []any{},
			"monitoring": false, "ebs_optimized": true, "source_dest_check": true, "tenancy": "default",
			"cpu_options":                        []any{map[string]any{"core_count": 1, "threads_per_core": 2, "amd_sev_snp": ""}},
			"capacity_reservation_specification": []any{map[string]any{"capacity_reservation_preference": "open", "capacity_reservation_target": []any{}}},
			"maintenance_options":                []any{map[string]any{"auto_recovery": "default"}},
			"private_dns_name_options":           []any{map[string]any{"enable_resource_name_dns_a_record": false, "enable_resource_name_dns_aaaa_record": false, "hostname_type": "ip-name"}},
			"timeouts":                           nil, "volume_tags": nil, "user_data_replace_on_change": false, "hibernation": false,
		}
		resources = append(resources, res{Mode: "managed", Type: "aws_instance", Name: fmt.Sprintf("app_%d", i), Provider: `provider["registry.opentofu.org/hashicorp/aws"]`,
			Instances: []inst{{SchemaVersion: 1, Attributes: attrs, Sensitive: []any{}, Dependencies: []string{"aws_security_group.app", "aws_subnet.a"}}}})
	}
	_ = vpc
	st := map[string]any{"version": 4, "terraform_version": "1.10.0", "serial": 42, "lineage": "x", "outputs": map[string]any{}, "resources": resources}
	b, _ := json.Marshal(st)
	return b
}

type extractor struct {
	mac     []byte
	kept    map[uint64]struct{}
	perRes  int
	scanned int
}

func (e *extractor) add(v string) {
	if e.perRes >= 64 {
		return
	}
	m := hmac.New(sha256.New, e.mac)
	m.Write([]byte(v))
	s := m.Sum(nil)
	e.kept[binary.BigEndian.Uint64(s[:8])] = struct{}{}
	e.perRes++
}

func (e *extractor) walk(v any, depth int) {
	if depth > 16 {
		return
	}
	switch x := v.(type) {
	case string:
		if len(x) > 65536 {
			return
		}
		switch {
		case shortRE.MatchString(x), len(x) <= 2048 && arnRE.MatchString(x):
			e.add(x)
		case len(x) > 0 && (x[0] == '{' || x[0] == '['):
			e.scanned += len(x)
			for _, m := range arnIn.FindAllString(x, 64) {
				e.add(m)
			}
		}
	case []any:
		for _, y := range x {
			e.walk(y, depth+1)
		}
	case map[string]any:
		for _, y := range x {
			e.walk(y, depth+1)
		}
	}
}

func main() {
	n := flag.Int("n", 20000, "resources")
	flag.Parse()
	r := &rng{s: 1}
	b := gen(r, *n)
	fmt.Printf("state bytes %d, bytes/resource %d\n", len(b), len(b)/(*n))
	runtime.GC()
	t := time.Now()
	var st struct {
		Resources []struct {
			Mode      string `json:"mode"`
			Instances []struct {
				Attributes map[string]any `json:"attributes"`
			} `json:"instances"`
		} `json:"resources"`
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	if err := dec.Decode(&st); err != nil {
		panic(err)
	}
	td := time.Since(t)
	e := &extractor{mac: []byte("k"), kept: map[uint64]struct{}{}}
	for _, res := range st.Resources {
		for _, in := range res.Instances {
			e.perRes = 0
			e.walk(map[string]any(in.Attributes), 0)
		}
	}
	keys := make([]uint64, 0, len(e.kept))
	for k := range e.kept {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	tt := time.Since(t)
	mb := float64(len(b)) / 1e6
	fmt.Printf("decode %v, total %v, %.1f MB/s, values kept %d (%.2f per resource), json scanned %d bytes\n", td, tt, mb/tt.Seconds(), len(keys), float64(len(keys))/float64(*n), e.scanned)
	os.Exit(0)
}
