package caption

const (
	cc608SpecialCh1   byte = 0x11 // CEA-608-C Table 5: Special Characters prefix (CC1)
	cc608ExtSpaMisc   byte = 0x12 // CEA-608-C Table 6: Extended Spanish/Misc + French prefix
	cc608ExtPorGerCh1 byte = 0x13 // CEA-608-C Table 7: Extended Portuguese/German prefix
)

type runeKind uint8

const (
	runeKindBasic runeKind = iota
	runeKindAsciiSeq
	runeKindSpecial
	runeKindExtended
	runeKindUnsupported
	runeKindSkip
)

type runeMapping struct {
	kind     runeKind
	basic    byte
	asciiSeq string
	twoByte  [2]byte
	// fallback is the closest standard (basic) ASCII byte for runeKindExtended
	// mappings. Per CEA-608-E §8.4 a conformant decoder auto-backspaces over the
	// preceding character when it receives a 0x12/0x13 extended code, so the
	// encoder must transmit this fallback basic char first; the decoder shows it
	// on non-extended players and backspaces over it on extended players. Zero
	// for non-extended kinds (special 0x11 codes do NOT auto-backspace).
	fallback byte
}

// CEA-608-C basic NA character set: ASCII slots 0x2A, 0x5C-0x60, 0x7B-0x7E carry Spanish chars.
var runeToBasic = map[rune]byte{
	'\u00E1': 0x2A, // á
	'\u00E9': 0x5C, // é
	'\u00ED': 0x5E, // í
	'\u00F3': 0x5F, // ó
	'\u00FA': 0x60, // ú
	'\u00E7': 0x7B, // ç
	'\u00F7': 0x7C, // ÷
	'\u00D1': 0x7D, // Ñ
	'\u00F1': 0x7E, // ñ
}

// CEA-608-C Table 5: Special Characters (0x11 0x3x on CC1).
var runeToSpecial = map[rune]byte{
	'\u00AE': 0x30, // ®
	'\u00B0': 0x31, // °
	'\u00BD': 0x32, // ½
	'\u00BF': 0x33, // ¿
	'\u2122': 0x34, // ™
	'\u00A2': 0x35, // ¢
	'\u00A3': 0x36, // £
	'\u266A': 0x37, // ♪
	'\u00E0': 0x38, // à
	'\u00E8': 0x3A, // è
	'\u00E2': 0x3B, // â
	'\u00EA': 0x3C, // ê
	'\u00EE': 0x3D, // î
	'\u00F4': 0x3E, // ô
	'\u00FB': 0x3F, // û
}

// CEA-608-C Table 6: Extended Spanish/Misc + French (0x12 0x2x or 0x12 0x3x on CC1).
var runeToExtSpaMisc = map[rune]byte{
	'\u00C1': 0x20, // Á
	'\u00C9': 0x21, // É
	'\u00D3': 0x22, // Ó
	'\u00DA': 0x23, // Ú
	'\u00DC': 0x24, // Ü
	'\u00FC': 0x25, // ü
	'\u00A1': 0x27, // ¡
	'\u00A9': 0x2B, // ©
	'\u2120': 0x2C, // ℠
	'\u2022': 0x2D, // •
	'\u00C0': 0x30, // À
	'\u00C2': 0x31, // Â
	'\u00C7': 0x32, // Ç
	'\u00C8': 0x33, // È
	'\u00CA': 0x34, // Ê
	'\u00CB': 0x35, // Ë
	'\u00EB': 0x36, // ë
	'\u00CE': 0x37, // Î
	'\u00CF': 0x38, // Ï
	'\u00EF': 0x39, // ï
	'\u00D4': 0x3A, // Ô
	'\u00D9': 0x3B, // Ù
	'\u00F9': 0x3C, // ù
	'\u00DB': 0x3D, // Û
	'\u00AB': 0x3E, // «
	'\u00BB': 0x3F, // »
}

// CEA-608-C Table 7: Extended Portuguese/German/Danish (0x13 0x2x or 0x13 0x3x on CC1).
var runeToExtPorGer = map[rune]byte{
	'\u00C3': 0x20, // Ã
	'\u00E3': 0x21, // ã
	'\u00CD': 0x22, // Í
	'\u00CC': 0x23, // Ì
	'\u00EC': 0x24, // ì
	'\u00D2': 0x25, // Ò
	'\u00F2': 0x26, // ò
	'\u00D5': 0x27, // Õ
	'\u00F5': 0x28, // õ
	'\u00C4': 0x30, // Ä
	'\u00E4': 0x31, // ä
	'\u00D6': 0x32, // Ö
	'\u00F6': 0x33, // ö
	'\u00DF': 0x34, // ß
	'\u00A5': 0x35, // ¥
	'\u00A4': 0x36, // ¤
	'\u00C5': 0x38, // Å
	'\u00E5': 0x39, // å
	'\u00D8': 0x3A, // Ø
	'\u00F8': 0x3B, // ø
	'\u250C': 0x3C, // ┌
	'\u2510': 0x3D, // ┐
	'\u2514': 0x3E, // └
	'\u2518': 0x3F, // ┘
}

// extRuneToFallback maps each CEA-608 extended-table rune (0x12/0x13 codes) to
// the closest standard ASCII character. Extended codes trigger an automatic
// decoder backspace (CEA-608-E \u00a78.4), so this fallback basic char is transmitted
// immediately before the extended pair: it is rendered by decoders that lack the
// extended glyph and backspaced over by decoders that have it. Without it the
// extended char would backspace over the previous legitimate character. Keys
// must stay in sync with runeToExtSpaMisc and runeToExtPorGer.
var extRuneToFallback = map[rune]byte{
	// runeToExtSpaMisc (0x12 table).
	'\u00c1': 'A', // \u00c1
	'\u00c9': 'E', // \u00c9
	'\u00d3': 'O', // \u00d3
	'\u00da': 'U', // \u00da
	'\u00dc': 'U', // \u00dc
	'\u00fc': 'u', // \u00fc
	'\u00a1': '!', // \u00a1
	'\u00a9': 'C', // \u00a9
	'\u2120': 'S', // \u2120
	'\u2022': '*', // \u2022
	'\u00c0': 'A', // \u00c0
	'\u00c2': 'A', // \u00c2
	'\u00c7': 'C', // \u00c7
	'\u00c8': 'E', // \u00c8
	'\u00ca': 'E', // \u00ca
	'\u00cb': 'E', // \u00cb
	'\u00eb': 'e', // \u00eb
	'\u00ce': 'I', // \u00ce
	'\u00cf': 'I', // \u00cf
	'\u00ef': 'i', // \u00ef
	'\u00d4': 'O', // \u00d4
	'\u00d9': 'U', // \u00d9
	'\u00f9': 'u', // \u00f9
	'\u00db': 'U', // \u00db
	'\u00ab': '"', // \u00ab
	'\u00bb': '"', // \u00bb

	// runeToExtPorGer (0x13 table).
	'\u00c3': 'A', // \u00c3
	'\u00e3': 'a', // \u00e3
	'\u00cd': 'I', // \u00cd
	'\u00cc': 'I', // \u00cc
	'\u00ec': 'i', // \u00ec
	'\u00d2': 'O', // \u00d2
	'\u00f2': 'o', // \u00f2
	'\u00d5': 'O', // \u00d5
	'\u00f5': 'o', // \u00f5
	'\u00c4': 'A', // \u00c4
	'\u00e4': 'a', // \u00e4
	'\u00d6': 'O', // \u00d6
	'\u00f6': 'o', // \u00f6
	'\u00df': 's', // \u00df
	'\u00a5': 'Y', // \u00a5
	'\u00a4': '*', // \u00a4
	'\u00c5': 'A', // \u00c5
	'\u00e5': 'a', // \u00e5
	'\u00d8': 'O', // \u00d8
	'\u00f8': 'o', // \u00f8
	'\u250c': '+', // \u250c
	'\u2510': '+', // \u2510
	'\u2514': '+', // \u2514
	'\u2518': '+', // \u2518
}

var runeToAsciiSeq = map[rune]string{
	'\u2018': "'",
	'\u2019': "'",
	'\u201A': "'",
	'\u201B': "'",
	'\u201C': "\"",
	'\u201D': "\"",
	'\u201E': "\"",
	'\u2014': "--",
	'\u2013': "-",
	'\u2012': "-",
	'\u2015': "--",
	'\u2026': "...",
	'\u00A0': " ",
	'\u202F': " ",
	'\u2009': " ",
	'\u200A': " ",
}

// Diacritic-strip fallback for Latin chars with no CEA-608 entry.
var runeToStrip = map[rune]byte{
	'\u0105': 'a', '\u0103': 'a', '\u0101': 'a',
	'\u0107': 'c', '\u010D': 'c', '\u0109': 'c',
	'\u010F': 'd', '\u0111': 'd',
	'\u0117': 'e', '\u0119': 'e', '\u011B': 'e', '\u0113': 'e', '\u0115': 'e',
	'\u011F': 'g', '\u0121': 'g', '\u0123': 'g',
	'\u0125': 'h', '\u0127': 'h',
	'\u0129': 'i', '\u012B': 'i', '\u012D': 'i', '\u012F': 'i', '\u0131': 'i',
	'\u0135': 'j',
	'\u0137': 'k',
	'\u013A': 'l', '\u013C': 'l', '\u013E': 'l', '\u0142': 'l',
	'\u0144': 'n', '\u0146': 'n', '\u0148': 'n',
	'\u014D': 'o', '\u014F': 'o', '\u0151': 'o',
	'\u0155': 'r', '\u0157': 'r', '\u0159': 'r',
	'\u015B': 's', '\u015D': 's', '\u015F': 's', '\u0161': 's',
	'\u0163': 't', '\u0165': 't',
	'\u0169': 'u', '\u016B': 'u', '\u016D': 'u', '\u016F': 'u', '\u0171': 'u', '\u0173': 'u',
	'\u0175': 'w',
	'\u0177': 'y', '\u00FD': 'y', '\u00FF': 'y',
	'\u017A': 'z', '\u017C': 'z', '\u017E': 'z',

	'\u0104': 'A', '\u0102': 'A', '\u0100': 'A',
	'\u0106': 'C', '\u010C': 'C', '\u0108': 'C',
	'\u010E': 'D', '\u0110': 'D',
	'\u0116': 'E', '\u0118': 'E', '\u011A': 'E', '\u0112': 'E', '\u0114': 'E',
	'\u011E': 'G', '\u0120': 'G', '\u0122': 'G',
	'\u0124': 'H', '\u0126': 'H',
	'\u0128': 'I', '\u012A': 'I', '\u012C': 'I', '\u012E': 'I', '\u0130': 'I',
	'\u0134': 'J',
	'\u0136': 'K',
	'\u0139': 'L', '\u013B': 'L', '\u013D': 'L', '\u0141': 'L',
	'\u0143': 'N', '\u0145': 'N', '\u0147': 'N',
	'\u014C': 'O', '\u014E': 'O', '\u0150': 'O',
	'\u0154': 'R', '\u0156': 'R', '\u0158': 'R',
	'\u015A': 'S', '\u015C': 'S', '\u015E': 'S', '\u0160': 'S',
	'\u0162': 'T', '\u0164': 'T',
	'\u0168': 'U', '\u016A': 'U', '\u016C': 'U', '\u016E': 'U', '\u0170': 'U', '\u0172': 'U',
	'\u0174': 'W',
	'\u0176': 'Y', '\u00DD': 'Y',
	'\u0179': 'Z', '\u017B': 'Z', '\u017D': 'Z',
}

func lookupRune(r rune) runeMapping {
	if r < 0x20 || r == 0x7F {
		return runeMapping{kind: runeKindSkip}
	}
	// CEA-608-C reuses ASCII slots 0x2A and 0x5C-0x60 and 0x7B-0x7E for Spanish
	// chars on a strict decoder (* renders as á, \ as é, ~ as ñ, etc). Modern
	// consumer decoders restore ASCII per the CEA-708 extension, matching what
	// pycaption and FFmpeg readeia608 emit. We accept the trade-off so English
	// captions stay byte-clean; for strict-decoder targets, route the affected
	// chars via the 0x13 extended table.
	if r >= 0x20 && r <= 0x7E {
		return runeMapping{kind: runeKindBasic, basic: byte(r)}
	}
	if b, ok := runeToBasic[r]; ok {
		return runeMapping{kind: runeKindBasic, basic: b}
	}
	if b, ok := runeToSpecial[r]; ok {
		return runeMapping{kind: runeKindSpecial, twoByte: [2]byte{cc608SpecialCh1, b}}
	}
	if b, ok := runeToExtSpaMisc[r]; ok {
		return runeMapping{kind: runeKindExtended, twoByte: [2]byte{cc608ExtSpaMisc, b}, fallback: extFallback(r)}
	}
	if b, ok := runeToExtPorGer[r]; ok {
		return runeMapping{kind: runeKindExtended, twoByte: [2]byte{cc608ExtPorGerCh1, b}, fallback: extFallback(r)}
	}
	if s, ok := runeToAsciiSeq[r]; ok {
		return runeMapping{kind: runeKindAsciiSeq, asciiSeq: s}
	}
	if b, ok := runeToStrip[r]; ok {
		return runeMapping{kind: runeKindBasic, basic: b}
	}
	return runeMapping{kind: runeKindUnsupported}
}

// extFallback returns the standard ASCII byte transmitted before an extended
// (0x12/0x13) code so the decoder's automatic backspace consumes it instead of
// the previous on-screen character. Defaults to a space for any extended rune
// missing from extRuneToFallback — a space is a benign backspace target that
// still preserves the preceding character.
func extFallback(r rune) byte {
	if b, ok := extRuneToFallback[r]; ok {
		return b
	}
	return ' '
}
