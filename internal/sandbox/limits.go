package sandbox

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

const (
	MemoryLabel    = "io.github.sandboxed-agents.memory"
	CPUsLabel      = "io.github.sandboxed-agents.cpus"
	PIDsLimitLabel = "io.github.sandboxed-agents.pids-limit"
	ShmSizeLabel   = "io.github.sandboxed-agents.shm-size"
)

type resourceLimit struct {
	option   string
	label    string
	parse    func(string) (string, error)
	value    string
	provided bool
	given    string
}

type ResourceLimits struct {
	limits []resourceLimit
}

func ParseUpOptions(args []string) (ResourceLimits, int, error) {
	limits := ResourceLimits{limits: []resourceLimit{
		{option: "--memory", label: MemoryLabel, value: "8589934592", parse: canonicalMemory},
		{option: "--cpus", label: CPUsLabel, value: "4", parse: canonicalCPUs},
		{option: "--pids-limit", label: PIDsLimitLabel, value: "2048", parse: canonicalPIDs},
		{option: "--shm-size", label: ShmSizeLabel, value: "1073741824", parse: canonicalShmSize},
	}}
	portOption := resourceLimit{option: "--port", parse: canonicalSSHPort}
	for index := 0; index < len(args); index++ {
		option, value, inline := strings.Cut(args[index], "=")
		var limit *resourceLimit
		for i := range limits.limits {
			if limits.limits[i].option == option {
				limit = &limits.limits[i]
				break
			}
		}
		if option == portOption.option {
			limit = &portOption
		}
		if limit == nil {
			kind := limitsUnexpectedArgument
			if strings.HasPrefix(args[index], "-") {
				kind = limitsUnknownOption
			}
			return ResourceLimits{}, 0, fmt.Errorf(limitsArgumentError, kind, args[index])
		}
		if limit.provided {
			return ResourceLimits{}, 0, fmt.Errorf(limitsDuplicateError, option)
		}
		if !inline {
			index++
			if index == len(args) {
				return ResourceLimits{}, 0, fmt.Errorf(limitsMissingValueError, option)
			}
			value = args[index]
		}
		canonical, err := limit.parse(value)
		if err != nil {
			if limit == &portOption {
				return ResourceLimits{}, 0, fmt.Errorf("invalid value %q for --port; use a whole number from 1 through 65535", value)
			}
			return ResourceLimits{}, 0, fmt.Errorf(limitsInvalidValueError, option, value)
		}
		limit.value, limit.given, limit.provided = canonical, value, true
	}
	port := 0
	if portOption.provided {
		port, _ = strconv.Atoi(portOption.value)
	}
	return limits, port, nil
}

func canonicalSSHPort(value string) (string, error) {
	port, err := parseSSHPort(value)
	return strconv.Itoa(port), err
}

var (
	integerLimitPattern = regexp.MustCompile(`^[0-9]+$`)
	sizeLimitPattern    = regexp.MustCompile(`^[0-9]+[kKmMgGtT]?$`)
	cpuLimitPattern     = regexp.MustCompile(`^[0-9]+(\.[0-9]{1,3})?$`)
)

func canonicalCPUs(value string) (string, error) {
	if !cpuLimitPattern.MatchString(value) {
		return "", strconv.ErrSyntax
	}
	whole, fraction, _ := strings.Cut(value, ".")
	whole = strings.TrimLeft(whole, "0")
	if whole == "" {
		whole = "0"
	}
	fraction += strings.Repeat("0", 9-len(fraction))
	nano, err := strconv.ParseInt(whole+fraction, 10, 64)
	if err != nil || nano <= 0 {
		return "", strconv.ErrRange
	}
	fraction = strings.TrimRight(fraction, "0")
	if fraction == "" {
		return whole, nil
	}
	return whole + "." + fraction, nil
}

func canonicalMemory(value string) (string, error)  { return canonicalSize(value, 6291456) }
func canonicalShmSize(value string) (string, error) { return canonicalSize(value, 1) }

func canonicalSize(value string, minimum int64) (string, error) {
	if !sizeLimitPattern.MatchString(value) {
		return "", strconv.ErrSyntax
	}
	multiplier := int64(1)
	if power := strings.Index("kmgt", strings.ToLower(value[len(value)-1:])); power >= 0 {
		multiplier = int64(1) << (10 * (power + 1))
		value = value[:len(value)-1]
	}
	number, err := positiveInteger(value)
	if err != nil || number > math.MaxInt64/multiplier || number*multiplier < minimum {
		return "", strconv.ErrRange
	}
	return strconv.FormatInt(number*multiplier, 10), nil
}

func canonicalPIDs(value string) (string, error) {
	number, err := positiveInteger(value)
	if err != nil {
		return "", err
	}
	return strconv.FormatInt(number, 10), nil
}

func positiveInteger(value string) (int64, error) {
	if !integerLimitPattern.MatchString(value) {
		return 0, strconv.ErrSyntax
	}
	number, err := strconv.ParseInt(value, 10, 64)
	if err != nil || number <= 0 {
		return 0, strconv.ErrRange
	}
	return number, nil
}

func (limits ResourceLimits) createArguments() []string {
	var args []string
	for _, limit := range limits.limits {
		args = append(args, limit.option+"="+limit.value,
			"--label", limit.label+"="+limit.value)
	}
	return args
}

func (limits ResourceLimits) checkRecorded(name string, labels map[string]string) error {
	for _, limit := range limits.limits {
		if !limit.provided {
			continue
		}
		recorded, present := labels[limit.label]
		canonical, err := limit.parse(recorded)
		if !present || err != nil || canonical != limit.value {
			return fmt.Errorf(limitsConflictError, limit.option, recorded, limit.given, name, name)
		}
	}
	return nil
}
