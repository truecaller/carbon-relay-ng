package metrics20

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

type metricVersion int

const (
	Legacy      metricVersion = iota // bar.bytes or whatever
	M20                              // foo=bar.unit=B
	M20NoEquals                      // foo_is_bar.unit_is_B
)

func (version metricVersion) TagDelimiter() string {
	if version == M20 {
		return "="
	} else if version == M20NoEquals {
		return "_is_"
	}
	panic("TagDelimiter() called on metricVersion" + string(version))
}

// getVersion returns the expected version of a metric, but doesn't validate
func GetVersion(metric_in string) metricVersion {
	if strings.Contains(metric_in, "=") {
		return M20
	}
	if strings.Contains(metric_in, "_is_") {
		return M20NoEquals
	}
	return Legacy
}

var byteEquals = []byte("=")
var byteIs = []byte("_is_")

// getVersionB is like getVersion but for byte array input.
func GetVersionB(metric_in []byte) metricVersion {
	if bytes.Contains(metric_in, byteEquals) {
		return M20
	}
	if bytes.Contains(metric_in, byteIs) {
		return M20NoEquals
	}
	return Legacy
}

func IsMetric20(metric_in string) bool {
	v := GetVersion(metric_in)
	return v == M20 || v == M20NoEquals
}

// ValidateSensibleChars checks that the metric id onlay contains characters that
// are commonly understood to be sensible and useful.  Because Graphite will do
// the weirdest things with all kinds of special characters.
func ValidateSensibleChars(metric_id string) error {
	for _, ch := range metric_id {
		if !(ch >= 'a' && ch <= 'z') && !(ch >= 'A' && ch <= 'Z') && !(ch >= '0' && ch <= '9') && ch != '_' && ch != '-' && ch != '.' {
			return fmt.Errorf("metric '%s' contains illegal char '%s'", metric_id, string(ch))
		}
	}
	return nil
}

// ValidateSensibleCharsB is like ValidateSensibleChars but for byte array inputs.
func ValidateSensibleCharsB(metric_id []byte) error {
	for _, ch := range metric_id {
		if !(ch >= 'a' && ch <= 'z') && !(ch >= 'A' && ch <= 'Z') && !(ch >= '0' && ch <= '9') && ch != '_' && ch != '-' && ch != '.' {
			return fmt.Errorf("metric '%s' contains illegal char '%s'", string(metric_id), string(ch))
		}
	}
	return nil
}

// InitialValidation checks the basic form of metric keys
func InitialValidation(metric_id string, version metricVersion) error {
	if version == Legacy {
		// if the metric contains no = or _is_, in theory we don't really care what it does contain.  it can be whatever.
		// in practice, graphite alters (removes a dot) the metric id when this happens:
		if strings.Contains(metric_id, "..") {
			return fmt.Errorf("metric '%s' has an empty node", metric_id)
		}
		return ValidateSensibleChars(metric_id)
	}
	if version == M20 {
		if strings.Contains(metric_id, "_is_") {
			return fmt.Errorf("metric '%s' has both = and _is_", metric_id)
		}
		if !strings.HasPrefix(metric_id, "unit=") && !strings.Contains(metric_id, ".unit=") {
			return fmt.Errorf("metric '%s' has no unit tag", metric_id)
		}
		if !strings.HasPrefix(metric_id, "target_type=") && !strings.Contains(metric_id, ".target_type=") {
			return fmt.Errorf("metric '%s' has no target_type tag", metric_id)
		}
	} else { //version == M20NoEquals
		if strings.Contains(metric_id, "=") {
			return fmt.Errorf("metric '%s' has both = and _is_", metric_id)
		}
		if !strings.HasPrefix(metric_id, "unit_is_") && !strings.Contains(metric_id, ".unit_is_") {
			return fmt.Errorf("metric '%s' has no unit tag", metric_id)
		}
		if !strings.HasPrefix(metric_id, "target_type_is_") && !strings.Contains(metric_id, ".target_type_is_") {
			return fmt.Errorf("metric '%s' has no target_type tag", metric_id)
		}
	}
	if strings.Count(metric_id, ".") < 2 {
		return fmt.Errorf("metric '%s': must have at least one tag_k/tag_v pair beyond unit and target_type", metric_id)
	}
	return nil
}

// optimization so compiler doesn't initialize and allocate new variables every time we use this.
// shouldn't be needed for the strings above because they are immutable, I'm assuming the compiler optimizes for that
var (
	doubleDot    = []byte("..")
	m20Is        = []byte("_is_")
	m20UnitPre   = []byte("unit=")
	m20UnitMid   = []byte(".unit=")
	m20TTPre     = []byte("target_type=")
	m20TTMid     = []byte(".target_type=")
	m20NEIS      = []byte("=")
	m20NEUnitPre = []byte("unit_is_")
	m20NEUnitMid = []byte(".unit_is_")
	m20NETTPre   = []byte("target_type_is_")
	m20NETTMid   = []byte(".target_type_is_")
	dot          = []byte(".")
)

// InitialValidationB is like InitialValidation but for byte array inputs.
func InitialValidationB(metric_id []byte, version metricVersion) error {
	if version == Legacy {
		if bytes.Contains(metric_id, doubleDot) {
			return fmt.Errorf("metric '%s' has an empty node", metric_id)
		}
		return ValidateSensibleCharsB(metric_id)
	}
	if version == M20 {
		if bytes.Contains(metric_id, m20Is) {
			return fmt.Errorf("metric '%s' has both = and _is_", metric_id)
		}
		if !bytes.HasPrefix(metric_id, m20UnitPre) && !bytes.Contains(metric_id, m20UnitMid) {
			return fmt.Errorf("metric '%s' has no unit tag", metric_id)
		}
		if !bytes.HasPrefix(metric_id, m20TTPre) && !bytes.Contains(metric_id, m20TTMid) {
			return fmt.Errorf("metric '%s' has no target_type tag", metric_id)
		}
	} else { //version == M20NoEquals
		if bytes.Contains(metric_id, m20NEIS) {
			return fmt.Errorf("metric '%s' has both = and _is_", metric_id)
		}
		if !bytes.HasPrefix(metric_id, m20NEUnitPre) && !bytes.Contains(metric_id, m20NEUnitMid) {
			return fmt.Errorf("metric '%s' has no unit tag", metric_id)
		}
		if !bytes.HasPrefix(metric_id, m20NETTPre) && !bytes.Contains(metric_id, m20NETTMid) {
			return fmt.Errorf("metric '%s' has no target_type tag", metric_id)
		}
	}
	if bytes.Count(metric_id, dot) < 2 {
		return fmt.Errorf("metric '%s': must have at least one tag_k/tag_v pair beyond unit and target_type", metric_id)
	}
	return nil
}

var space = []byte(" ")

// ValidatePacket validates a carbon message.
func ValidatePacket(buf []byte) error {
	fields := bytes.Fields(buf)
	if len(fields) != 3 {
		return errors.New("packet must consist of 3 fields")

	}

	version := GetVersionB(fields[0])
	err := InitialValidationB(fields[0], version)
	if err != nil {
		return err
	}

	_, err = strconv.ParseFloat(string(fields[1]), 32)
	if err != nil {
		return errors.New("value field is not a float or int")
	}

	_, err = strconv.ParseUint(string(fields[2]), 10, 0)
	if err != nil {
		return errors.New("timestamp field is not a unix timestamp")
	}

	return nil
}

type MetricSpec struct {
	Id   string
	Tags map[string]string
}

// NewMetricSpec takes a metric key, validates it (unit tag, etc) and
// converts it to a MetricSpec, setting nX tags, cleans up ps to /s unit
func NewMetricSpec(id string) (metric *MetricSpec, err error) {
	version := GetVersion(id)
	err = InitialValidation(id, version)
	if err != nil {
		return nil, err
	}
	nodes := strings.Split(id, ".")
	del := version.TagDelimiter()
	tags := make(map[string]string)
	for i, node := range nodes {
		tag := strings.Split(node, del)
		if len(tag) > 2 {
			return nil, errors.New("bad metric spec: more than 1 equals")
		} else if len(tag) < 2 {
			tags[fmt.Sprintf("n%d", i+1)] = node
		} else if tag[0] == "" || tag[1] == "" {
			return nil, errors.New("bad metric spec: tag_k and tag_v must be non-empty strings")
		} else {
			// k=v format, and both are != ""
			key := tag[0]
			val := tag[1]
			if _, ok := tags[key]; ok {
				return nil, fmt.Errorf("duplicate tag key '%s'", key)
			}
			if key == "unit" && strings.HasSuffix(val, "ps") {
				val = val[:len(val)-2] + "/s"
			}
			tags[key] = val
		}
	}
	return &MetricSpec{id, tags}, nil
}

type MetricEs struct {
	Tags []string `json:"tags"`
}

func NewMetricEs(spec MetricSpec) MetricEs {
	tags := make([]string, len(spec.Tags), len(spec.Tags))
	i := 0
	for tag_key, tag_val := range spec.Tags {
		tags[i] = fmt.Sprintf("%s=%s", tag_key, tag_val)
		i++
	}
	return MetricEs{tags}
}
