package matching

import (
	"encoding/json"
	"errors"
)

type jsonGroup struct {
	Name     string `json:"name"`
	MinSize  int    `json:"min_size"`
	Capacity int    `json:"capacity"`
	Members  []int  `json:"members"`
}

type jsonPerson struct {
	Name        string `json:"name"`
	Preferences []int  `json:"preferences"`
}

type jsonStore struct {
	Groups  []jsonGroup  `json:"groups"`
	Persons []jsonPerson `json:"persons"`
}

func ToJSON(groups []*Group, persons []*Person) ([]byte, error) {
	jsonGroups := make([]jsonGroup, len(groups))
	jsonPersons := make([]jsonPerson, len(persons))
	for i := range persons {
		jsonPersons[i] = jsonPerson{Name: persons[i].Name, Preferences: make([]int, len(persons[i].Preferences))}
	}
	for i, group := range groups {
		jsonGroups[i] = jsonGroup{Name: group.Name, MinSize: group.MinSize, Capacity: group.Capacity, Members: make([]int, len(group.Members))}
		for j, member := range group.Members {
			jsonGroups[i].Members[j] = member.IndexIn(persons)
		}
	}
	for i := range persons {
		for j, pref := range persons[i].Preferences {
			jsonPersons[i].Preferences[j] = pref.IndexIn(groups)
		}
	}
	jsonS := jsonStore{Groups: jsonGroups, Persons: jsonPersons}
	return json.Marshal(jsonS)
}

func FromJSON(encoded []byte) (groups []*Group, persons []*Person, err error) {
	store := jsonStore{}
	err = json.Unmarshal(encoded, &store)
	if err != nil {
		return
	}
	jsonGroups := store.Groups
	jsonPersons := store.Persons
	groups = make([]*Group, len(jsonGroups))
	persons = make([]*Person, len(jsonPersons))
	for i := range jsonPersons {
		persons[i] = &Person{Name: jsonPersons[i].Name, Preferences: make([]*Group, len(jsonPersons[i].Preferences))}
	}
	for i := range jsonGroups {
		groups[i] = &Group{Name: jsonGroups[i].Name, MinSize: jsonGroups[i].MinSize, Capacity: jsonGroups[i].Capacity, Members: make([]*Person, len(jsonGroups[i].Members))}
		for j, k := range jsonGroups[i].Members {
			if k < 0 || k >= len(persons) {
				return nil, nil, errors.New("Person index out of range!")
			}
			groups[i].Members[j] = persons[k]
		}
	}
	for i := range jsonPersons {
		for j, k := range jsonPersons[i].Preferences {
			if k < 0 || k >= len(groups) {
				return nil, nil, errors.New("Group index out of range!")
			}
			persons[i].Preferences[j] = groups[k]
		}
	}
	return
}
