package matching

import (
	"math/rand"
	"sort"
	"strings"
)

type Person struct {
	Name        string
	Preferences []*Group
}

func NewPerson(name string, preferences []*Group) *Person {
	return &Person{Name: name, Preferences: preferences}
}

func (p *Person) String() string {
	return p.Name
}

func (value *Person) IndexIn(slice []*Person) int {
	for p, v := range slice {
		if v == value {
			return p
		}
	}
	return -1
}

func (p *Person) GetGroup(groups []*Group) *Group {
	for _, g := range groups {
		if p.IndexIn(g.Members) != -1 {
			return g
		}
	}
	return nil
}

func Shuffle(a []*Person) {
	for i := range a {
		j := rand.Intn(i + 1)
		a[i], a[j] = a[j], a[i]
	}
}

type slicePerson []*Person

func (s slicePerson) Len() int {
	return len(s)
}

func (s slicePerson) Less(i, j int) bool {
	return strings.Compare(s[i].Name, s[j].Name) < 0
}

func (s slicePerson) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}

func Sort(s []*Person) {
	var t slicePerson = s
	sort.Sort(t)
}

func FindPerson(name string, persons []*Person) *Person {
	for i := 0; i < len(persons); i++ {
		if persons[i].Name == name {
			return persons[i]
		}
	}
	return nil
}
