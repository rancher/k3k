package cmds

import "fmt"

type change struct {
	field    string
	oldValue string
	newValue string
}

func printDiff(changes []change) {
	for _, c := range changes {
		if c.oldValue == c.newValue {
			continue
		}

		fmt.Printf("%s: %s -> %s\n", c.field, c.oldValue, c.newValue)
	}
}

func printMapDiff(title string, changes []change) {
	if len(changes) == 0 {
		return
	}

	fmt.Printf("%s:\n", title)

	for _, c := range changes {
		switch c.oldValue {
		case "":
			fmt.Printf("  %s=%s (new)\n", c.field, c.newValue)
		default:
			fmt.Printf("  %s=%s -> %s=%s\n", c.field, c.oldValue, c.field, c.newValue)
		}
	}
}

func diffMaps(oldMap, newMap map[string]string) []change {
	var changes []change

	// Check for new and changed keys
	for k, newVal := range newMap {
		if oldVal, exists := oldMap[k]; exists {
			if oldVal != newVal {
				changes = append(changes, change{k, oldVal, newVal})
			}
		} else {
			changes = append(changes, change{k, "", newVal})
		}
	}

	return changes
}
