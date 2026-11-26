package companion

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/iotest"
	"time"
)

const (
	redactionStamp  = "11/22/2026 18:04:11.212+4  "
	redactionHeader = redactionStamp + "COMBAT_LOG_VERSION,22,ADVANCED_LOG_ENABLED,1,BUILD_VERSION,1.60.1,PROJECT_ID,18\n"
	redactionMap    = redactionStamp + `MAP_CHANGE,1429,"Elwynn, Eastern Kingdoms",-10.5,0,-9,0` + "\n"

	redactionNPCGUID  = "Creature-0-1111-0-2414-618-0000AB1234"
	redactionNPC      = redactionNPCGUID + `,"Sentry, ""Old Guard""",0xa48,0x0`
	redactionSource   = `Player-9001-0AA10001,"SyntheticSource-ExampleRealm",0x511,0x0`
	redactionTarget   = `Player-9001-0AA10002,"SyntheticTarget-ExampleRealm",0x512,0x0`
	redactionCaster   = `Player-9002-0AA10003,"SyntheticCaster, \"Shield\"-ExampleRealm",0x514,0x0`
	redactedSource    = `Player-0-00000000,"",0x511,0x0`
	redactedTarget    = `Player-0-00000000,"",0x512,0x0`
	redactedCaster    = `Player-0-00000000,"",0x514,0x0`
	redactionSpell    = `585,"Smite, Rank 1",0x2`
	redactionShield   = `17,"Power Word: Shield",0x2`
	redactionNPCStats = redactionNPCGUID + ",0000000000000000,310,450,12,4,77,0,0,0,-1,0,0,0,-9071.25,100.75,1429,1.25,7"
	redactionDamage   = "40,55,-1,2,0,0,0,nil,nil,nil"
)

func combatRedactionRecord(event, source, target, details string) string {
	line := redactionStamp + event + "," + source + "," + target
	if details != "" {
		line += "," + details
	}
	return line + "\n"
}

func assertFilteredCombatLog(t *testing.T, input, want string, events int) {
	t.Helper()
	got, n, err := FilterCombatLog(strings.NewReader(input))
	if err != nil || n != events || string(got) != want {
		t.Fatalf("got %d events (%v):\n%s\nwant %d events:\n%s", n, err, got, events, want)
	}
}

func TestFilterCombatLogAbsorbActorLayouts(t *testing.T) {
	npcCaster := `Creature-0-1111-0-2414-619-0000AB1235,"Shield Keeper",0xa48,0x0`
	for _, tc := range []struct {
		name, event, source, target, safeSource, safeTarget, prefix, caster, safeCaster, amounts string
	}{
		{"spell from NPC with separate player caster", "SPELL_ABSORBED", redactionNPC, redactionTarget, redactionNPC, redactedTarget,
			redactionSpell + ",", redactionCaster, redactedCaster, "25,70,nil"},
		{"swing from NPC with separate player caster", "SPELL_ABSORBED", redactionNPC, redactionTarget, redactionNPC, redactedTarget,
			"", redactionCaster, redactedCaster, "25,70"},
		{"spell from player with NPC defender and caster", "SPELL_ABSORBED", redactionSource, redactionNPC, redactedSource, redactionNPC,
			redactionSpell + ",", npcCaster, npcCaster, "25"},
		{"swing from player with self-shielded NPC", "SPELL_ABSORBED", redactionSource, redactionNPC, redactedSource, redactionNPC,
			"", redactionNPC, redactionNPC, "25,nil,70"},
		{"heal with separate player caster", "SPELL_HEAL_ABSORBED", redactionSource, redactionNPC, redactedSource, redactionNPC,
			redactionSpell + ",", redactionCaster, redactedCaster, "25,70"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			shield := "," + redactionShield + "," + tc.amounts
			input := redactionHeader + combatRedactionRecord(tc.event, tc.source, tc.target, tc.prefix+tc.caster+shield)
			want := redactionHeader + combatRedactionRecord(tc.event, tc.safeSource, tc.safeTarget, tc.prefix+tc.safeCaster+shield)
			assertFilteredCombatLog(t, input, want, 1)
		})
	}
}

func TestFilterCombatLogKnownEventSchemas(t *testing.T) {
	for _, tc := range []struct{ events, details string }{
		{"SWING_DAMAGE SWING_DAMAGE_LANDED", redactionNPCStats + "," + redactionDamage},
		{"SPELL_DAMAGE SPELL_PERIODIC_DAMAGE RANGE_DAMAGE DAMAGE_SHIELD DAMAGE_SPLIT SPELL_BUILDING_DAMAGE",
			redactionSpell + "," + redactionNPCStats + "," + redactionDamage + ",ST"},
		{"SWING_MISSED", "PARRY,nil"},
		{"SPELL_MISSED SPELL_PERIODIC_MISSED RANGE_MISSED DAMAGE_SHIELD_MISSED", redactionSpell + ",ABSORB,nil,25,55,nil,AOE"},
		{"SPELL_HEAL SPELL_PERIODIC_HEAL SPELL_BUILDING_HEAL", redactionSpell + "," + redactionNPCStats + ",42,60,18,0,1"},
		{"SPELL_CAST_SUCCESS", redactionSpell + "," + redactionNPCStats},
		{"SPELL_CAST_START SPELL_CREATE SPELL_SUMMON SPELL_RESURRECT SPELL_DURABILITY_DAMAGE SPELL_DURABILITY_DAMAGE_ALL SPELL_EMPOWER_START", redactionSpell},
		{"SPELL_ENERGIZE SPELL_PERIODIC_ENERGIZE SPELL_LEECH SPELL_PERIODIC_LEECH SPELL_DRAIN SPELL_PERIODIC_DRAIN",
			redactionSpell + "," + redactionNPCStats + ",20.25,0.0,1,100"},
		{"SPELL_EXTRA_ATTACKS SPELL_EMPOWER_END SPELL_EMPOWER_INTERRUPT", redactionSpell + ",2"},
		{"SPELL_AURA_APPLIED SPELL_AURA_REMOVED SPELL_AURA_REFRESH SPELL_AURA_BROKEN", redactionSpell + ",BUFF"},
		{"SPELL_AURA_APPLIED_DOSE SPELL_AURA_REMOVED_DOSE", redactionSpell + ",DEBUFF,3"},
		{"SPELL_INTERRUPT SPELL_DISPEL_FAILED", redactionSpell + "," + redactionShield},
		{"SPELL_DISPEL SPELL_STOLEN SPELL_AURA_BROKEN_SPELL", redactionSpell + "," + redactionShield + ",BUFF"},
		{"SPELL_INSTAKILL", redactionSpell + ",nil"},
		{"UNIT_DIED UNIT_DESTROYED", "0,nil"},
		{"PARTY_KILL", ""},
	} {
		for _, event := range strings.Fields(tc.events) {
			t.Run(event, func(t *testing.T) {
				input := redactionHeader +
					combatRedactionRecord(event, redactionSource, redactionNPC, tc.details) +
					combatRedactionRecord(event, redactionNPC, redactionTarget, tc.details)
				want := redactionHeader +
					combatRedactionRecord(event, redactedSource, redactionNPC, tc.details) +
					combatRedactionRecord(event, redactionNPC, redactedTarget, tc.details)
				assertFilteredCombatLog(t, input, want, 2)
			})
		}
	}
}

func TestFilterCombatLogRedactsCastFailureText(t *testing.T) {
	input := combatRedactionRecord("SPELL_CAST_FAILED", redactionSource, redactionNPC,
		redactionSpell+`,"SyntheticFailure, spell unavailable"`)
	want := combatRedactionRecord("SPELL_CAST_FAILED", redactedSource, redactionNPC, redactionSpell+`,""`)
	assertFilteredCombatLog(t, redactionHeader+input, redactionHeader+want, 1)
}

func TestFilterCombatLogRedactsAdvancedUnitAndOwner(t *testing.T) {
	petGUID := "Pet-0-1111-0-2414-416-0000AB2222"
	guardianGUID := "Creature-0-1111-0-2414-417-0000AB3333"
	ownerGUID := "Player-9002-0AA10004"
	stats := ",100,143,12,4,77,0,0,0,-1,0,0,0,-8999.04,123.24,1429,1.2,67"
	safeStats := ",0,0,0,0,0,0,0,0,0,0,0,0,0.0,0.0,0,0.0,0"
	for _, tc := range []struct{ name, guid, actor, owner, safeActor, safeStats string }{
		{"player", "Player-9001-0AA10002", redactionTarget, "0000000000000000", redactedTarget,
			"Player-0-00000000,0000000000000000" + safeStats},
		{"pet", petGUID, petGUID + `,"SyntheticPet",0x1111,0x0`, ownerGUID, `Pet-0-0-0-0-0-0000000000,"",0x1111,0x0`,
			"Pet-0-0-0-0-0-0000000000,Player-0-00000000" + safeStats},
		{"player-controlled creature", guardianGUID, guardianGUID + `,"SyntheticGuardian",0x2111,0x0`, petGUID,
			`Pet-0-0-0-0-0-0000000000,"",0x2111,0x0`, "Pet-0-0-0-0-0-0000000000,Pet-0-0-0-0-0-0000000000" + safeStats},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := redactionHeader + combatRedactionRecord("SPELL_DAMAGE", redactionNPC, tc.actor,
				redactionSpell+","+tc.guid+","+tc.owner+stats+","+redactionDamage)
			want := redactionHeader + combatRedactionRecord("SPELL_DAMAGE", redactionNPC, tc.safeActor,
				redactionSpell+","+tc.safeStats+","+redactionDamage)
			assertFilteredCombatLog(t, input, want, 1)
			assertFilteredCombatLog(t, want, want, 1)
		})
	}
}

func TestFilterCombatLogKeepsKnownAdvancedLayouts(t *testing.T) {
	for _, tc := range []struct{ name, version, buildAndProject, stats, damage string }{
		{"legacy Classic", "9", "1.13.4,PROJECT_ID,2", "-1,0,0,0", "40,-1,2,0,0,0,nil,nil,nil"},
		{"Classic Era", "9", "1.15.9,PROJECT_ID,2", "0,0,-1,0,0,0", "40,-1,2,0,0,0,nil,nil,nil,AOE"},
		{"Shadowlands", "19", "9.1.5,PROJECT_ID,1", "0,-1,0,0,0", redactionDamage},
		{"Dragonflight", "20", "10.2.7,PROJECT_ID,1", "0,-1,0,0,0", redactionDamage},
		{"legacy format 22", "22", "11.2.0,PROJECT_ID,1", "0,-1,0,0,0", redactionDamage + ",ST"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			header := redactionStamp + "COMBAT_LOG_VERSION," + tc.version + ",ADVANCED_LOG_ENABLED,1,BUILD_VERSION," + tc.buildAndProject + "\n"
			details := redactionSpell + "," + redactionNPCGUID + ",0000000000000000,310,450,12,4,77," +
				tc.stats + ",-9071.25,100.75,1429,1.25,7," + tc.damage
			input := header + combatRedactionRecord("SPELL_DAMAGE", redactionSource, redactionNPC, details)
			want := header + combatRedactionRecord("SPELL_DAMAGE", redactedSource, redactionNPC, details)
			assertFilteredCombatLog(t, input, want, 1)
		})
	}
}

func TestFilterCombatLogOmitsUnreviewedRecords(t *testing.T) {
	spellAbsorb := combatRedactionRecord("SPELL_ABSORBED", redactionNPC, redactionTarget,
		redactionSpell+","+redactionCaster+","+redactionShield+",25,70,nil")
	swingAbsorb := combatRedactionRecord("SPELL_ABSORBED", redactionNPC, redactionTarget, redactionCaster+","+redactionShield+",25")
	cast := combatRedactionRecord("SPELL_CAST_START", redactionSource, redactionNPC, redactionSpell)
	safe := combatRedactionRecord("SPELL_CAST_START", redactedSource, redactionNPC, redactionSpell)
	damage := combatRedactionRecord("SPELL_DAMAGE", redactionSource, redactionNPC, redactionSpell+","+redactionNPCStats+","+redactionDamage)
	ambiguousStats := strings.Replace(redactionNPCStats, ",-9071.25,100.75,1429,1.25,7", ",1,1,1,1,1", 1)
	ambiguousDamage := combatRedactionRecord("SPELL_DAMAGE", redactionSource, redactionNPC,
		redactionSpell+","+ambiguousStats+",40,0,2,0,0,0,nil,nil,nil")
	for _, tc := range []struct{ name, record string }{
		{"extra absorb field", strings.TrimSuffix(spellAbsorb, "\n") + ",\"SyntheticExtra\"\n"},
		{"extra numeric absorb field", strings.TrimSuffix(spellAbsorb, "\n") + ",9\n"},
		{"missing swing absorb amount", strings.Replace(swingAbsorb, ",25\n", "\n", 1)},
		{"missing spell absorb amount", strings.Replace(spellAbsorb, ",25,70,nil", "", 1)},
		{"truncated spell absorb", strings.Replace(spellAbsorb, ","+redactionShield+",25,70,nil", "", 1)},
		{"untyped absorb amount", strings.Replace(spellAbsorb, ",25,70,nil", ",\"SyntheticAmount\",70,nil", 1)},
		{"missing caster GUID", strings.Replace(swingAbsorb, "Player-9002-0AA10003", "", 1)},
		{"invalid caster GUID", strings.Replace(spellAbsorb, "Player-9002-0AA10003", "Player-invalid", 1)},
		{"unknown caster GUID type", strings.Replace(spellAbsorb, "Player-9002-0AA10003", "Unknown-9002-0AA10003", 1)},
		{"missing caster name field", strings.Replace(spellAbsorb, redactionCaster, "Player-9002-0AA10003,,0x514,0x0", 1)},
		{"invalid caster flags", strings.Replace(spellAbsorb, ",0x514,", ",nil,", 1)},
		{"unclosed quote", strings.Replace(cast, redactionSpell, `585,"Smite,0x2`, 1)},
		{"stray unquoted quote", strings.Replace(cast, redactionSpell, `585,Sm"ite,0x2`, 1)},
		{"characters after quote", strings.Replace(cast, redactionSpell, `585,"Smite"extra,0x2`, 1)},
		{"extra spell field", strings.TrimSuffix(cast, "\n") + ",\"SyntheticExtra\"\n"},
		{"trailing empty field", strings.TrimSuffix(cast, "\n") + ",\n"},
		{"GUID in game label", strings.Replace(cast, "Smite, Rank 1", "Player-9002-0AA10003", 1)},
		{"unsupported prefix match", strings.Replace(cast, "SPELL_CAST_START", "SPELL_UNREVIEWED", 1)},
		{"unsupported death event", strings.Replace(cast, "SPELL_CAST_START", "UNIT_DIED_EXTRA", 1)},
		{"unsupported support event", strings.Replace(spellAbsorb, "SPELL_ABSORBED", "SPELL_ABSORBED_SUPPORT", 1)},
		{"arbitrary timestamp", strings.Replace(cast, redactionStamp, "SyntheticHeader  ", 1)},
		{"unreviewed map field", strings.TrimSuffix(redactionMap, "\n") + ",\"SyntheticMapExtra\"\n"},
		{"untyped advanced field", strings.Replace(damage, ",310,", ",\"SyntheticStat\",", 1)},
		{"unrecognized advanced width", strings.Replace(damage, ",310,", ",310,310,", 1)},
		{"ambiguous advanced width", ambiguousDamage},
		{"invalid UTF-8", strings.Replace(cast, "Smite, Rank 1", "Smite\xff", 1)},
		{"control byte", strings.Replace(cast, "Smite, Rank 1", "Smite\x00", 1)},
		{"no mob", combatRedactionRecord("SPELL_ABSORBED", redactionSource, redactionTarget, redactionCaster+","+redactionShield+",25")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assertFilteredCombatLog(t, redactionHeader+tc.record+cast, redactionHeader+safe, 1)
		})
	}
}

func TestFilterCombatLogPreservesQuotedFieldsAndVersionBoundaries(t *testing.T) {
	cast := combatRedactionRecord("SPELL_CAST_START", redactionNPC, redactedTarget, `585,"Smite, \"Rank One\"",0x2`)
	disabled := strings.Replace(redactionHeader, "ENABLED,1", "ENABLED,0", 1)
	invalid := strings.TrimSuffix(redactionHeader, "\n") + ",\"SyntheticHeaderExtra\"\n"
	unknown := strings.Replace(redactionHeader, "VERSION,22,", "VERSION,999,", 1)
	input := "\ufeff" + strings.ReplaceAll(redactionHeader+redactionMap+cast+disabled+cast+redactionHeader+cast+invalid+cast+unknown+cast, "\n", "\r\n")
	input += redactionHeader + strings.TrimSuffix(cast, "\n")
	assertFilteredCombatLog(t, input, redactionHeader+redactionMap+cast+redactionHeader+cast+redactionHeader+cast, 3)
}

func TestFilterCombatLogReadErrorDiscardsOutput(t *testing.T) {
	readErr := errors.New("test read error")
	log := redactionHeader + combatRedactionRecord("SWING_MISSED", redactionNPC, redactionTarget, "PARRY,nil")
	r := io.MultiReader(strings.NewReader(log), iotest.ErrReader(readErr))
	out, events, err := FilterCombatLog(r)
	if !errors.Is(err, readErr) || out != nil || events != 0 {
		t.Fatalf("got %d bytes, %d events, %v; want no output and the read error", len(out), events, err)
	}
}

func TestCombatLogUploadBundleRedaction(t *testing.T) {
	server := &fakeServer{token: "test-token"}
	httpServer := httptest.NewServer(server.handler(t))
	t.Cleanup(httpServer.Close)
	root := t.TempDir()
	edition := filepath.Join(root, "_forever_")
	logDir := filepath.Join(edition, "Logs")
	for _, dir := range []string{filepath.Join(edition, "WTF", "Account", "ACC"), logDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	validDetails := redactionSpell + "," + redactionCaster + "," + redactionShield + ",25,70,nil"
	safeDetails := redactionSpell + "," + redactedCaster + "," + redactionShield + ",25,70,nil"
	valid := combatRedactionRecord("SPELL_ABSORBED", redactionNPC, redactionTarget, validDetails)
	invalid := combatRedactionRecord("SPELL_ABSORBED", redactionNPC, redactionTarget, validDetails+`,"SyntheticExtra"`)
	want := redactionHeader + redactionMap + combatRedactionRecord("SPELL_ABSORBED", redactionNPC, redactedTarget, safeDetails)
	name := "WoWCombatLog-112226_180411.txt"
	logs := map[string]string{
		filepath.Join(logDir, name):                             redactionHeader + redactionMap + valid + invalid,
		filepath.Join(logDir, "WoWCombatLog-112226_180417.txt"): redactionHeader + invalid,
	}
	clock := time.Date(2026, 11, 22, 20, 0, 0, 0, time.UTC)
	for path, raw := range logs {
		if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
			t.Fatal(err)
		}
		old := clock.Add(-time.Hour)
		if err := os.Chtimes(path, old, old); err != nil {
			t.Fatal(err)
		}
	}
	e, err := NewEngine(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	e.now = func() time.Time { return clock }
	e.discover = func([]string) []SavedVarFile { return nil }
	e.discoverEditions = DiscoverEditions
	cfg := e.Config()
	cfg.ServerURL, cfg.AutoUpload = httpServer.URL, true
	cfg.WoWRoots = []string{root}
	cfg.ShareAddons = map[string]bool{combatLogKey: true}
	if err := e.UpdateConfig(cfg); err != nil {
		t.Fatal(err)
	}
	e.RunOnce(context.Background(), false, false)
	modes, kinds, uploads := server.uploads()
	files := e.Status().AddonFiles
	if server.puts.Load() != 1 || len(uploads) != 1 || len(files) != 2 {
		t.Fatalf("got %d requests and %d ZIPs for %d logs, want one upload for two logs", server.puts.Load(), len(uploads), len(files))
	}
	m, payload := bundleLua(t, uploads[0])
	if payload != want {
		t.Fatalf("ZIP log:\n%s\nwant:\n%s", payload, want)
	}
	zr, err := zip.NewReader(bytes.NewReader(uploads[0]), int64(len(uploads[0])))
	if err != nil {
		t.Fatal(err)
	}
	if len(zr.File) != 2 || zr.File[0].Name != "manifest.json" || zr.File[1].Name != name {
		t.Fatal("ZIP must contain the manifest and filtered combat log")
	}
	sum := sha256.Sum256([]byte(payload))
	hash := hex.EncodeToString(sum[:])
	if kinds[0] != KindAddon || modes[0] != ModeFull || m.Kind != KindAddon || m.Addon != combatLogKey ||
		m.Mode != ModeFull || m.Edition != "forever" || m.FileName != name || m.Rows != 3 ||
		m.Bytes != int64(len(want)) || m.SHA256 != hash || m.FileSHA256 != hash {
		t.Fatalf("unexpected manifest: %+v", m)
	}
	headers := server.headers[0]
	if headers.Get("X-WoWCensus-Lua-Bytes") != strconv.Itoa(len(want)) ||
		headers.Get("X-WoWCensus-Sha256") != hash ||
		headers.Get("X-WoWCensus-Addon") != combatLogKey {
		t.Fatalf("upload headers do not match the filtered payload: %v", headers)
	}
	for path, raw := range logs {
		after, err := os.ReadFile(path)
		if err != nil || string(after) != raw {
			t.Fatalf("source log %s changed: %v", path, err)
		}
	}
}
