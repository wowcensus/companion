package companion

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	anonymousPlayer         = "Player-0-00000000"
	anonymousPet            = "Pet-0-0-0-0-0-0000000000"
	noCombatActor           = "0000000000000000"
	combatPlayerControlFlag = 0x100
	combatPlayerTypeFlag    = 0x400
	combatPetTypeFlag       = 0x1000
	combatPlayerFlags       = combatPlayerControlFlag | combatPlayerTypeFlag | combatPetTypeFlag
)

var (
	combatTimestampRe = regexp.MustCompile(`^(0?[1-9]|1[0-2])/(0?[1-9]|[12][0-9]|3[01])(/[0-9]{4})? ([01]?[0-9]|2[0-3]):[0-5][0-9]:[0-5][0-9]\.[0-9]{3}([+-]?[0-9]{1,2})?$`)
	combatBuildRe     = regexp.MustCompile(`^[0-9]+(\.[0-9]+){2,3}$`)
	combatPlayerRe    = regexp.MustCompile(`^Player-[0-9]+-[0-9A-Fa-f]+$`)
	combatUnitRe      = regexp.MustCompile(`^(Creature|Pet|Vehicle|GameObject)-([0-9]+-){5}[0-9A-Fa-f]+$`)
	combatIdentityRe  = regexp.MustCompile(`(?i)(Player-[0-9]+-|Pet-([0-9]+-){5})[0-9a-f]+`)
)

func splitCombatFields(s string, fields []string) []string {
	for {
		if len(fields) == cap(fields) {
			return nil
		}
		end := 0
		if len(s) > 0 && s[0] == '"' {
			end = combatQuoteEnd(s)
			if end < 0 || end < len(s) && s[end] != ',' {
				return nil
			}
		} else {
			for end < len(s) && s[end] != ',' {
				if s[end] == '"' || s[end] < ' ' {
					return nil
				}
				end++
			}
		}
		fields = append(fields, s[:end])
		if end == len(s) {
			return fields
		}
		s = s[end+1:]
	}
}

func combatQuoteEnd(s string) int {
	for i := 1; i < len(s); i++ {
		if s[i] < ' ' {
			return -1
		}
		switch s[i] {
		case '\\':
			if i+1 < len(s) && (s[i+1] == '"' || s[i+1] == '\\') {
				i++
			}
		case '"':
			if i+1 < len(s) && s[i+1] == '"' {
				i++
			} else {
				return i + 1
			}
		}
	}
	return -1
}

type combatFields []func(string) bool

func (schema combatFields) matches(fields []string) bool {
	if len(fields) != len(schema) {
		return false
	}
	for i, valid := range schema {
		if !valid(fields[i]) {
			return false
		}
	}
	return true
}

func combatInt(s string) bool {
	_, err := strconv.ParseInt(s, 10, 64)
	return err == nil
}

func combatUint(s string) bool {
	_, err := strconv.ParseUint(s, 10, 32)
	return err == nil
}

func combatNumber(s string) bool {
	n, err := strconv.ParseFloat(s, 64)
	return err == nil && !math.IsNaN(n) && !math.IsInf(n, 0)
}

func combatIntOrNil(s string) bool {
	return s == "nil" || combatInt(s)
}

func parseCombatMask(s string) (uint64, bool) {
	base := 10
	if strings.HasPrefix(s, "0x") || strings.HasPrefix(s, "0X") {
		s, base = s[2:], 16
	}
	n, err := strconv.ParseUint(s, base, 32)
	return n, err == nil
}

func combatMask(s string) bool {
	_, ok := parseCombatMask(s)
	return ok
}

func combatBool(s string) bool {
	return s == "nil" || s == "0" || s == "1"
}

func combatQuoted(s string) bool {
	return len(s) >= 2 && s[0] == '"' && utf8.ValidString(s)
}

func combatLabel(s string) bool {
	return len(s) > 2 && combatQuoted(s) && !combatIdentityRe.MatchString(s)
}

func combatAura(s string) bool {
	return s == "BUFF" || s == "DEBUFF"
}

func combatMiss(s string) bool {
	switch s {
	case "MISS", "DODGE", "PARRY", "BLOCK", "DEFLECT", "IMMUNE", "ABSORB", "REFLECT", "EVADE", "RESIST":
		return true
	}
	return false
}

func combatHint(s string) bool {
	return s == "ST" || s == "AOE"
}

func combatGUIDKind(guid string) (string, bool) {
	switch guid {
	case "nil", noCombatActor:
		return "", true
	}
	kind, _, _ := strings.Cut(guid, "-")
	switch kind {
	case "Player":
		return kind, combatPlayerRe.MatchString(guid)
	case "Creature", "Pet", "Vehicle", "GameObject":
		return kind, combatUnitRe.MatchString(guid)
	}
	return "", false
}

type combatActor struct {
	original string
	guid     string
}

func filterCombatActor(fields []string) (combatActor, bool) {
	guid := fields[0]
	kind, validGUID := combatGUIDKind(guid)
	flags, validFlags := parseCombatMask(fields[2])
	if !validGUID || !validFlags || !combatMask(fields[3]) {
		return combatActor{}, false
	}
	name := fields[1]
	if name != "nil" && (!combatQuoted(name) || kind == "" && name != `""`) {
		return combatActor{}, false
	}
	switch {
	case kind == "Player":
		fields[0], fields[1] = anonymousPlayer, `""`
	case kind == "Pet" || kind != "" && flags&combatPlayerFlags != 0:
		fields[0], fields[1] = anonymousPet, `""`
	case combatIdentityRe.MatchString(name):
		return combatActor{}, false
	}
	return combatActor{original: guid, guid: fields[0]}, true
}

var (
	combatSpell         = combatFields{combatUint, combatLabel, combatMask}
	combatMap           = combatFields{combatUint, combatLabel, combatNumber, combatNumber, combatNumber, combatNumber}
	combatAbsorbAmounts = []combatFields{
		{combatUint},
		{combatUint, combatUint},
		{combatUint, combatUint, combatBool},
		{combatUint, combatBool, combatUint},
	}
)

type combatEventLayout struct {
	spell    bool
	advanced bool
	hint     bool
	suffixes []combatFields
}

var (
	combatDamageLegacy = combatFields{
		combatInt, combatInt, combatMask, combatIntOrNil, combatIntOrNil, combatIntOrNil,
		combatBool, combatBool, combatBool,
	}
	combatDamageRaw      = append(combatFields{combatInt}, combatDamageLegacy...)
	combatDamageSuffixes = []combatFields{
		combatDamageLegacy, append(combatDamageLegacy, combatBool),
		combatDamageRaw, append(combatDamageRaw, combatBool),
	}
	combatMissSuffixes = []combatFields{
		{combatMiss}, {combatMiss, combatBool}, {combatMiss, combatBool, combatIntOrNil},
		{combatMiss, combatBool, combatIntOrNil, combatBool},
		{combatMiss, combatBool, combatIntOrNil, combatIntOrNil, combatBool},
	}
	combatHealSuffixes = []combatFields{
		{combatInt, combatInt, combatIntOrNil, combatBool},
		{combatInt, combatInt, combatInt, combatIntOrNil, combatBool},
	}
	combatPowerSuffixes = []combatFields{
		{combatNumber, combatNumber, combatInt},
		{combatNumber, combatNumber, combatInt, combatNumber},
		{combatNumber, combatInt, combatNumber},
		{combatNumber, combatInt, combatNumber, combatNumber},
	}
	combatDeathSuffixes = []combatFields{nil, {combatUint}, {combatUint, combatBool}}
	combatEmptySuffix   = []combatFields{nil}
	combatAuraSuffixes  = []combatFields{{combatAura}, {combatAura, combatInt}}
	combatExtraAura     = combatFields{combatUint, combatLabel, combatMask, combatAura}
)

func combatLayout(event string) combatEventLayout {
	switch event {
	case "SWING_DAMAGE", "SWING_DAMAGE_LANDED":
		return combatEventLayout{advanced: true, suffixes: combatDamageSuffixes}
	case "SPELL_DAMAGE", "SPELL_PERIODIC_DAMAGE", "RANGE_DAMAGE", "DAMAGE_SHIELD", "DAMAGE_SPLIT", "SPELL_BUILDING_DAMAGE":
		return combatEventLayout{spell: true, advanced: true, hint: true, suffixes: combatDamageSuffixes}
	case "SWING_MISSED":
		return combatEventLayout{suffixes: combatMissSuffixes}
	case "SPELL_MISSED", "SPELL_PERIODIC_MISSED", "RANGE_MISSED", "DAMAGE_SHIELD_MISSED":
		return combatEventLayout{spell: true, hint: true, suffixes: combatMissSuffixes}
	case "SPELL_HEAL", "SPELL_PERIODIC_HEAL", "SPELL_BUILDING_HEAL":
		return combatEventLayout{spell: true, advanced: true, suffixes: combatHealSuffixes}
	case "SPELL_CAST_SUCCESS":
		return combatEventLayout{spell: true, advanced: true, suffixes: combatEmptySuffix}
	case "SPELL_CAST_START", "SPELL_SUMMON", "SPELL_CREATE", "SPELL_RESURRECT", "SPELL_DURABILITY_DAMAGE", "SPELL_DURABILITY_DAMAGE_ALL", "SPELL_EMPOWER_START":
		return combatEventLayout{spell: true, suffixes: combatEmptySuffix}
	case "SPELL_CAST_FAILED":
		return combatEventLayout{spell: true, suffixes: []combatFields{{combatQuoted}}}
	case "SPELL_ENERGIZE", "SPELL_PERIODIC_ENERGIZE", "SPELL_DRAIN", "SPELL_PERIODIC_DRAIN", "SPELL_LEECH", "SPELL_PERIODIC_LEECH":
		return combatEventLayout{spell: true, advanced: true, suffixes: combatPowerSuffixes}
	case "SPELL_EXTRA_ATTACKS", "SPELL_EMPOWER_END", "SPELL_EMPOWER_INTERRUPT":
		return combatEventLayout{spell: true, suffixes: []combatFields{{combatUint}}}
	case "SPELL_AURA_APPLIED", "SPELL_AURA_REMOVED", "SPELL_AURA_REFRESH", "SPELL_AURA_BROKEN":
		return combatEventLayout{spell: true, suffixes: combatAuraSuffixes}
	case "SPELL_AURA_APPLIED_DOSE", "SPELL_AURA_REMOVED_DOSE":
		return combatEventLayout{spell: true, suffixes: []combatFields{{combatAura, combatUint}}}
	case "SPELL_INTERRUPT":
		return combatEventLayout{spell: true, suffixes: []combatFields{combatSpell}}
	case "SPELL_DISPEL_FAILED":
		return combatEventLayout{spell: true, suffixes: []combatFields{combatSpell, combatExtraAura}}
	case "SPELL_DISPEL", "SPELL_STOLEN", "SPELL_AURA_BROKEN_SPELL":
		return combatEventLayout{spell: true, suffixes: []combatFields{combatExtraAura}}
	case "SPELL_INSTAKILL":
		return combatEventLayout{spell: true, suffixes: []combatFields{nil, {combatBool}}}
	case "UNIT_DIED", "UNIT_DESTROYED":
		return combatEventLayout{suffixes: combatDeathSuffixes}
	case "PARTY_KILL":
		return combatEventLayout{suffixes: combatEmptySuffix}
	}
	return combatEventLayout{}
}

func matchesCombatSuffix(fields []string, suffixes []combatFields) bool {
	for _, suffix := range suffixes {
		if suffix.matches(fields) {
			return true
		}
	}
	return false
}

func filterCombatRecord(fields []string, version string) bool {
	event := fields[0]
	switch event {
	case "COMBAT_LOG_VERSION":
		return len(fields) == 8 && combatAdvancedWidths(fields[1]) != nil && fields[2] == "ADVANCED_LOG_ENABLED" && fields[3] == "1" &&
			strings.TrimPrefix(fields[4], " ") == "BUILD_VERSION" && combatBuildRe.MatchString(fields[5]) &&
			fields[6] == "PROJECT_ID" && combatUint(fields[7])
	case "MAP_CHANGE":
		return combatMap.matches(fields[1:])
	}
	layout := combatLayout(event)
	isAbsorb := event == "SPELL_ABSORBED" || event == "SPELL_HEAL_ABSORBED"
	if len(fields) < 9 || layout.suffixes == nil && !isAbsorb {
		return false
	}
	source, validSource := filterCombatActor(fields[1:5])
	target, validTarget := filterCombatActor(fields[5:9])
	if !validSource || !validTarget || source.conflictsWith(target) ||
		!strings.HasPrefix(source.guid, "Creature-") && !strings.HasPrefix(target.guid, "Creature-") {
		return false
	}
	details := fields[9:]
	if isAbsorb {
		return filterCombatAbsorb(details, event == "SPELL_HEAL_ABSORBED", source, target)
	}
	if layout.spell {
		if len(details) < 3 || !combatSpell.matches(details[:3]) {
			return false
		}
		details = details[3:]
	}
	if n := len(details); layout.hint && n > 0 && combatHint(details[n-1]) {
		details = details[:n-1]
	}
	if matchesCombatSuffix(details, layout.suffixes) {
		if event == "SPELL_CAST_FAILED" {
			details[0] = `""`
		}
		return true
	}
	return layout.advanced && filterCombatAdvanced(details, layout.suffixes, version, source, target)
}

func (a combatActor) conflictsWith(b combatActor) bool {
	return a.original == b.original && a.guid != b.guid
}

func filterCombatAbsorb(fields []string, heal bool, source, target combatActor) bool {
	switch len(fields) {
	case 8, 9, 10:
		if heal {
			return false
		}
	case 11, 12, 13:
		if !combatSpell.matches(fields[:3]) {
			return false
		}
		fields = fields[3:]
	default:
		return false
	}
	if !combatSpell.matches(fields[4:7]) {
		return false
	}
	amounts := combatAbsorbAmounts
	if heal {
		amounts = amounts[:2]
	}
	if !matchesCombatSuffix(fields[7:], amounts) {
		return false
	}
	caster, valid := filterCombatActor(fields[:4])
	return valid && !caster.conflictsWith(source) && !caster.conflictsWith(target)
}

func combatAdvancedWidths(version string) []int {
	switch version {
	case "9":
		return []int{16, 18}
	case "19", "20":
		return []int{17}
	case "22":
		return []int{17, 19}
	}
	return nil
}

func filterCombatAdvanced(fields []string, suffixes []combatFields, version string, source, target combatActor) bool {
	width := 0
	for _, n := range combatAdvancedWidths(version) {
		if len(fields) >= n && matchesCombatSuffix(fields[n:], suffixes) && validCombatAdvancedStats(fields[2:n]) {
			if width != 0 {
				return false
			}
			width = n
		}
	}
	if width == 0 {
		return false
	}
	unit, owner := fields[0], fields[1]
	switch unit {
	case source.original:
		fields[0] = source.guid
	case target.original:
		fields[0] = target.guid
	case noCombatActor, "nil":
	default:
		return false
	}
	ownerKind, validOwner := combatGUIDKind(owner)
	if !validOwner {
		return false
	}
	switch {
	case owner == source.original:
		fields[1] = source.guid
	case owner == target.original:
		fields[1] = target.guid
	case ownerKind == "Player":
		fields[1] = anonymousPlayer
	case ownerKind == "Pet":
		fields[1] = anonymousPet
	case ownerKind != "":
		fields[1] = noCombatActor
	}
	privateUnit := fields[0] == anonymousPlayer || fields[0] == anonymousPet
	privateOwner := fields[1] == anonymousPlayer || fields[1] == anonymousPet
	if privateOwner && !privateUnit && unit != noCombatActor && unit != "nil" {
		return false
	}
	if privateUnit {
		for i := 2; i < width; i++ {
			fields[i] = "0"
		}
		fields[width-5], fields[width-4], fields[width-2] = "0.0", "0.0", "0.0"
	}
	return true
}

func validCombatAdvancedStats(fields []string) bool {
	numbers := len(fields) - 5
	for _, field := range fields[:numbers] {
		if !combatInt(field) {
			return false
		}
	}
	return combatNumber(fields[numbers]) && combatNumber(fields[numbers+1]) &&
		combatUint(fields[numbers+2]) && combatNumber(fields[numbers+3]) && combatInt(fields[numbers+4])
}
