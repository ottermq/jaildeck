package names

// The words names are made of (#148): an adjective and an animal, any
// pairing. Chosen by hand and reviewed, so that no pair reads badly. The
// adjectives are kind or neutral, and none is a word Jail Deck uses for a
// state -- no "stopped-", "failed-", "locked-", "running-", "ready-", "idle-"
// -- since a jail called stopped-otter would be misread on every page. The
// animals are ones most people can picture, and the otter's family is all
// here, as Otter Labs' should be.
//
// Every pairing fits in 20 characters, which leaves room for the "-NN" a
// suggestion gains on a host that has run out of pairs and still stays within
// 24. A test holds the lists to that, and to lowercase letters only.

var adjectives = []string{
	"able", "agile", "amber", "ample", "amused", "ardent", "astute", "azure",
	"balmy", "bold", "brave", "breezy", "bright", "brisk", "bubbly", "buoyant",
	"calm", "candid", "cheery", "chipper", "civil", "clever", "cobalt", "cosmic",
	"cozy", "crisp", "curious", "dapper", "daring", "dashing", "deft", "devoted",
	"dreamy", "eager", "earnest", "easy", "elated", "elegant", "epic", "even",
	"fair", "fancy", "fearless", "festive", "fine", "fluffy", "fond", "frank",
	"friendly", "frosty", "gallant", "gentle", "giddy", "gifted", "glad", "gleeful",
	"golden", "graceful", "grand", "grateful", "happy", "hardy", "hearty", "helpful",
	"honest", "humble", "jaunty", "jolly", "jovial", "joyful", "keen", "kind",
	"lively", "loyal", "lucid", "lucky", "lunar", "magic", "mellow", "merry",
	"mighty", "mindful", "modest", "musical", "nifty", "nimble", "noble", "patient",
	"peppy", "perky", "placid", "playful", "pleasant", "plucky", "polite", "proud",
	"quick", "quiet", "quirky", "radiant", "rapid", "regal", "relaxed", "rosy",
	"rustic", "sandy", "serene", "sharp", "shiny", "silent", "silver", "sincere",
	"smart", "smiling", "snappy", "snug", "solar", "sparkly", "spirited", "spry",
	"steady", "stellar", "stoic", "sturdy", "sunny", "swift", "tender", "thrifty",
	"tidy", "tranquil", "trusty", "upbeat", "valiant", "velvet", "vivid", "warm",
	"wise", "witty", "wondrous", "zany", "zealous", "zesty",
}

var animals = []string{
	// The otter's family first: Otter Labs, OtterMQ, Otter Cloud.
	"otter", "badger", "marten", "mink", "stoat", "weasel", "ermine", "ferret",
	"wolverine", "polecat", "fisher", "sable",

	"albatross", "alpaca", "antelope", "armadillo", "axolotl", "beaver", "bison",
	"bobcat", "buffalo", "camel", "capybara", "caribou", "cheetah", "chinchilla",
	"chipmunk", "condor", "cougar", "coyote", "crane", "cricket", "dingo", "dolphin",
	"donkey", "dove", "dragonfly", "eagle", "egret", "elephant", "elk", "falcon",
	"finch", "firefly", "flamingo", "fox", "gazelle", "gecko", "gibbon", "giraffe",
	"gopher", "gorilla", "hamster", "hare", "hawk", "hedgehog", "heron", "hippo",
	"hummingbird", "ibex", "iguana", "impala", "jackal", "jaguar", "jellyfish",
	"kangaroo", "kingfisher", "kiwi", "koala", "lemur", "leopard", "lion", "llama",
	"lobster", "lynx", "macaw", "manatee", "meerkat", "mongoose", "moose", "narwhal",
	"newt", "ocelot", "octopus", "okapi", "orca", "osprey", "ostrich", "owl",
	"panda", "panther", "parrot", "pelican", "penguin", "pheasant", "platypus",
	"porcupine", "puffin", "puma", "quail", "quokka", "rabbit", "raccoon", "raven",
	"reindeer", "robin", "salamander", "seahorse", "seal", "shark", "sparrow",
	"squirrel", "starling", "swan", "tapir", "tiger", "tortoise", "toucan", "turtle",
	"walrus", "whale", "wombat", "wren", "yak", "zebra",
}
