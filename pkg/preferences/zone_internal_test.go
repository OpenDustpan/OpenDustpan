package preferences

// AllowlistAccepts, ShapeAccepts and DatabaseAccepts expose the three layers
// of the time zone check, so that a test can send hostile input to one layer
// alone.
func AllowlistAccepts(name string) bool { return knownZone(name) }

func ShapeAccepts(name string) bool { return zoneShape(name) }

func DatabaseAccepts(name string) bool { return zoneLoads(name) }
