package main

// A binding affects automatic provider responses only, never the shared bundle
// cache or the manual gallery. Missing sets return no replacement artwork.
func (s *server) preferredBundle(typ, id string, b bundle) bundle {
	idSet := s.preferredSet(typ, id)
	if idSet == "" {
		return b
	}
	for _, group := range b.Sets {
		if group.ID == idSet {
			return bundle{Sets: []set{group}}
		}
	}
	return bundle{}
}
