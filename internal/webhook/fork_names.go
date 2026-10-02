package webhook

// Fork (Agenda Maestros 4x4): {nombre_corto} of the host's "faltan 5 minutos" notice - the
// client's first given name and first surname, as the owner asked ("Solo primer nombre y
// primer apellido").

import "strings"

// nameParticles join the word that follows them: "del Carmen", "de la Cruz", "y Gasset".
var nameParticles = map[string]bool{
	"de": true, "del": true, "la": true, "las": true, "los": true, "y": true,
	"da": true, "di": true, "van": true, "von": true,
}

// ShortName is the first given name + the first surname of full, with the capitals as the
// client typed them. Particles (de, del, la, las, los, y, da, di, van, von, any case) stick
// to the next word that is not one, so a compound counts as one word; a particle at the
// very end stays on its own. With n words: n >= 4 → the 1st and 3rd ("María Fernanda López
// García" → "María López"), n = 3 → the 1st and 2nd ("Juan Pérez García" → "Juan Pérez"),
// n = 2 → both, n = 1 → that one, nothing → "".
//
// Known limit: three words are read as name + two surnames, the common case, so "María
// Fernanda López" gives "María Fernanda".
func ShortName(full string) string {
	var words []string
	var pending []string
	for _, w := range strings.Fields(full) {
		if nameParticles[strings.ToLower(w)] {
			pending = append(pending, w)
			continue
		}
		words = append(words, strings.Join(append(pending, w), " "))
		pending = nil
	}
	if len(pending) > 0 {
		words = append(words, strings.Join(pending, " "))
	}
	switch n := len(words); {
	case n >= 4:
		return words[0] + " " + words[2]
	case n >= 2:
		return words[0] + " " + words[1]
	case n == 1:
		return words[0]
	}
	return ""
}
