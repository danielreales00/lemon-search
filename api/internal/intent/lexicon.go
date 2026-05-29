package intent

// rule is one lexicon entry's contribution to the overlay. Empty fields are
// no-ops when merged.
type rule struct {
	category    string   // sets Overlay.CategoryFilter (last write wins)
	subcategory []string // appended (union)
	universal   []string // appended (union)
	specific    []string // appended (union)
	price       []string // appended (union)
	openNow     bool     // OR-ed into RequireOpenNow
}

// Category / tag literals reused across entries (kept as consts for goconst and
// to keep the values matching the data exactly).
const (
	catFood    = "Food & Drinks"
	catEvents  = "Events"
	tagUpscale = "upscale"
	tagFamily  = "family-friendly"
)

// lexicon maps a normalized head term (unigram, or space-joined bigram) to its
// overlay contribution. Mirrors docs/ranking/intent.md. Frozen at init.
var lexicon = map[string]rule{
	// price family
	"cheap":      {price: []string{"$", "$$"}},
	"affordable": {price: []string{"$", "$$"}},
	"budget":     {price: []string{"$", "$$"}},
	"fancy":      {price: []string{"$$$", "$$$$"}, universal: []string{tagUpscale}},
	"upscale":    {price: []string{"$$$", "$$$$"}, universal: []string{tagUpscale}},
	"nice":       {universal: []string{tagUpscale}},

	// time family
	"open now":   {openNow: true},
	"tonight":    {openNow: true},
	"late night": {universal: []string{"late-night"}, specific: []string{"late-night-food"}},
	"happy hour": {specific: []string{"happy-hour"}},
	"brunch":     {specific: []string{"brunch"}},
	"breakfast":  {specific: []string{"breakfast"}},
	"lunch":      {specific: []string{"lunch"}},
	"dinner":     {specific: []string{"dinner"}},

	// audience family
	"date night":   {universal: []string{"date-night"}},
	"kid friendly": {universal: []string{"kid-friendly", tagFamily}},
	"family":       {universal: []string{tagFamily}},
	"solo":         {universal: []string{"solo-friendly"}},
	"group":        {universal: []string{"group-friendly"}},
	"tourist":      {universal: []string{"tourist-friendly"}},

	// setting family
	"outdoor":        {universal: []string{"outdoor-seating"}},
	"rooftop":        {specific: []string{"rooftop"}},
	"cozy":           {universal: []string{"cozy"}},
	"quiet":          {universal: []string{"quiet"}},
	"lively":         {universal: []string{"lively"}},
	"instagrammable": {universal: []string{"instagrammable"}},

	// domain pulls (category / subcategory narrowing)
	"wedding": {category: catEvents, subcategory: []string{
		"Weddings", "Photography & Video", "DJ / Music", "Florist", "Catering",
	}},
	"photographer":     {category: catEvents, subcategory: []string{"Photography & Video"}},
	"emergency":        {openNow: true},
	"tow":              {subcategory: []string{"Towing & Roadside"}},
	"personal trainer": {subcategory: []string{"Personal Training"}},
	"dog walker":       {subcategory: []string{"Walking"}},
	"cleaner":          {subcategory: []string{"Cleaning"}},

	// food domain (direct specific_tag match)
	"restaurant":  {category: catFood},
	"restaurants": {category: catFood},
	"sushi":       {category: catFood, specific: []string{"sushi"}},
	"tacos":       {category: catFood, specific: []string{"tacos"}},
	"coffee":      {category: catFood, specific: []string{"coffee"}},
	"pizza":       {category: catFood, specific: []string{"pizza"}},
	"burger":      {category: catFood, specific: []string{"burgers"}},
	"seafood":     {category: catFood, specific: []string{"seafood"}},
	"vegan":       {category: catFood, specific: []string{"vegetarian", "vegan"}},
	"cocktails":   {category: catFood, specific: []string{"cocktails"}},
	"wine":        {category: catFood, specific: []string{"wine"}},
	"beer":        {category: catFood, specific: []string{"beer"}},

	// idiomatic
	"hungry":     {category: catFood, openNow: true},
	"i'm hungry": {category: catFood, openNow: true},
}
