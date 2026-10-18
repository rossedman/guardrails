package lessons

import (
	"regexp"
	"strings"
)

// MaxPromptChars is the longest prompt Detect considers; longer ones are
// usually pasted logs or specs, not corrections.
const MaxPromptChars = 4000

// start matches the start of a clause: the text's start, a line start, or
// after sentence punctuation, a dash or "please".
const start = `(?:^|[.!?;:,\n]\s*|\s[-–—]\s*|\bplease\s+)`

type cue struct {
	signal string
	re     *regexp.Regexp
	// exclude rejects a match that only looks like the cue.
	exclude  *regexp.Regexp
	explicit bool
	// weak cues alone don't make a question a correction.
	weak bool
}

var cues = []cue{
	// Standing instructions.
	{signal: "from now on", re: regexp.MustCompile(`\b(from now on|going forward|moving forward)\b|\bin the future,?\s+(please\s+)?(always|never|don't|do not|use|make sure|remember)\b`), explicit: true},
	{signal: "remember", re: regexp.MustCompile(start + `(?:always\s+)?remember\s+(that|to)\b`), explicit: true},
	{signal: "in this repo", re: regexp.MustCompile(`\bin this (repo|repository|project|codebase),?\s+(we|you)\b`), explicit: true},
	{signal: "always", re: regexp.MustCompile(start + `always\s+[a-z]+`), explicit: true},
	{signal: "never", re: regexp.MustCompile(start + `never\s+[a-z]+`), exclude: regexp.MustCompile(`never\s+mind`), explicit: true},
	{signal: "convention", re: regexp.MustCompile(`\b(our|the) convention (here |in this \w+ )?is\b|\bby convention\b`), explicit: true},
	{signal: "we prefer", re: regexp.MustCompile(`\bwe (prefer|always|never)\b`), explicit: true},
	{signal: "we use", re: regexp.MustCompile(`\bwe (use|write|run)\s+[a-z]`), explicit: true, weak: true},

	// Corrections.
	{
		signal:  "no",
		re:      regexp.MustCompile(`^\s*(no|nope|nah)\s*[,.!:;–—-]?\s+(use|don't|do not|dont|stop|never|always|we|you|that's|thats|that is|it's|its|put|keep|call|write|run|make|it should|wrong|not)\b.*`),
		exclude: regexp.MustCompile(`^\s*(no|nope|nah)\s*[,.!:;–—-]?\s+((that's|thats|that is|it's|its)\s+)?(fine|ok|okay|good|great|perfect|correct|right|all good)\b|^\s*no\s+(worries|problem|thanks)\b`),
	},
	{signal: "don't", re: regexp.MustCompile(start + `(don't|do not|dont)\s+[a-z]+`), exclude: regexp.MustCompile(`(don't|do not|dont)\s+(know|understand|see|think|get|care|mind|worry|remember)$`)},
	{signal: "we don't", re: regexp.MustCompile(`\bwe (don't|do not|never)\s+[a-z]`)},
	{signal: "you don't need", re: regexp.MustCompile(`\byou (don't|do not) need to\b`)},
	{signal: "stop", re: regexp.MustCompile(start + `stop\s+[a-z]+ing\b`)},
	{signal: "avoid", re: regexp.MustCompile(start + `avoid\s+[a-z]`)},
	{signal: "that's wrong", re: regexp.MustCompile(`\b(that's|that is|thats|this is|it's|you're|you are)\s+(wrong|not right|incorrect|not what i (asked|wanted|meant))\b`)},
	{signal: "actually", re: regexp.MustCompile(start + `actually,?\s+(use|prefer|let's|lets|we|you should|it should|do|don't|make|put|keep|rename|move|change)\b`), weak: true},
	{signal: "instead", re: regexp.MustCompile(`\binstead of\b|\binstead\s*[.!]|\binstead\s*$`), weak: true},
	{signal: "i told you", re: regexp.MustCompile(`\bi (told|asked) you\b|\bi said (to|not)\b`)},
	{signal: "not like that", re: regexp.MustCompile(`\bnot like (that|this)\b`)},
	{signal: "why did you", re: regexp.MustCompile(`\bwhy (did|would|are) you\b`)},
	{signal: "revert", re: regexp.MustCompile(start + `(revert|undo)\s+(that|this|it|the|your|those|these)\b`)},
	{signal: "you shouldn't", re: regexp.MustCompile(`\byou (shouldn't|should not)\b`)},
}

// matches reports whether c matches text outside its exclusions.
func (c cue) matches(text string) bool {
	for _, m := range c.re.FindAllString(text, -1) {
		if c.exclude == nil || !c.exclude.MatchString(strings.TrimSpace(m)) {
			return true
		}
	}
	return false
}

var (
	fenced = regexp.MustCompile("(?s)```.*?(```|$)|~~~.*?(~~~|$)")
	inline = regexp.MustCompile("`[^`\n]*`")
	quoted = regexp.MustCompile(`"[^"\n]*"|“[^”\n]*”`)
	spaces = regexp.MustCompile(`[ \t]+`)
)

// Detect reports whether a prompt reads like a correction or a standing
// instruction, the cues that matched, and whether the user asked for it to
// be remembered. Code, quoted text, slash commands and long pastes are
// ignored.
func Detect(prompt string) (signals []string, explicit, ok bool) {
	text := strings.TrimSpace(prompt)
	if text == "" || strings.HasPrefix(text, "/") || len(text) > MaxPromptChars {
		return nil, false, false
	}
	prose := clean(text)
	// Mostly code or quotes: a paste, not an instruction.
	if len(strings.TrimSpace(prose))*10 < len(text)*3 {
		return nil, false, false
	}
	lower := strings.ToLower(prose)
	question := strings.HasSuffix(strings.TrimSpace(lower), "?")
	allWeak := true
	for _, c := range cues {
		if !c.matches(lower) {
			continue
		}
		signals = append(signals, c.signal)
		explicit = explicit || c.explicit
		allWeak = allWeak && c.weak
	}
	if len(signals) == 0 || (question && allWeak) {
		return nil, false, false
	}
	return signals, explicit, true
}

// clean drops code and quoted text and normalizes apostrophes, so cues
// only match what the user said in prose.
func clean(s string) string {
	s = fenced.ReplaceAllString(s, " ")
	s = inline.ReplaceAllString(s, " ")
	s = quoted.ReplaceAllString(s, " ")
	s = strings.NewReplacer("’", "'", "‘", "'").Replace(s)
	return spaces.ReplaceAllString(s, " ")
}
