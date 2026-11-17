package fasthtml

//go:generate go run ./internal/genshortcuts

// The one-call shortcuts (FindByNameClass, IsNameItemprop, ParentByName, AttributeTitle, ...) are generated
// into shortcuts_gen.go. They take a Tag; for names that are not in Tag use the Query methods:
// doc.Find(ByName("my-widget").Class("x")).

// The older "optional" shortcuts skip an empty criterion.

func optClass(q Query, class string) Query {
	if class == "" {
		return q
	}
	return q.Class(class)
}

func optID(q Query, id string) Query {
	if id == "" {
		return q
	}
	return q.ID(id)
}

func optText(q Query, text string) Query {
	if text == "" {
		return q
	}
	return q.Text(text)
}

func optBody(q Query, body string) Query {
	if body == "" {
		return q
	}
	return q.Body(body)
}

// FirstLevel returns the tags of the list at the level of the first one (the outermost tags of a FindAll result).
func FirstLevel(nodes []*Node) []*Node { return firstLevel(nodes, Any(), false) }

// FirstLevelByName returns the tags of the list with the name, at the level of the first tag of the list.
func FirstLevelByName(nodes []*Node, name Tag) []*Node { return firstLevel(nodes, By(name), true) }

// FirstLevelByNameClass returns the tags of the list with the name and the class, at the level of the first tag of the list.
func FirstLevelByNameClass(nodes []*Node, name Tag, class string) []*Node {
	return firstLevel(nodes, By(name).Class(class), true)
}

func firstLevel(nodes []*Node, q Query, check bool) []*Node {
	if len(nodes) == 0 {
		return nil
	}
	level := nodes[0].Depth()
	var result []*Node
	for _, n := range nodes {
		if n.Depth() == level && (!check || q.Matches(n)) {
			result = append(result, n)
		}
	}
	return result
}
