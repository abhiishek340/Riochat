package uicomponent

// Separators inside the stored description and props summary. The picker shows
// the whole text; prompt generation cuts both texts at these markers.
const (
	interactionMarker  = ". Interaction: "
	conventionMarker   = ". Convention: "
	imageURLConvention = "Image URLs may only come from tool results, content supplied by the user, or addresses that already appeared in this conversation (they must be https, or files under this site's /api/v1/files/ path); never invent them"
)

// builtinSpec is one entry of the product's built-in component list.
type builtinSpec struct {
	Name         string
	Description  string
	PropsSummary string
}

// builtinCatalog is the list the client can render natively, in display order.
// A row's sort order is its position here, starting at 1.
func builtinCatalog() []builtinSpec {
	return []builtinSpec{
		{
			Name:         "card-grid",
			Description:  "Card grid for items of one kind. Use when: showing several items with the same fields, such as news, search results or alternatives. Interaction: filter by tag; cards with a link open it on click; cover images open in a larger view",
			PropsSummary: `{title?, items:[{title, summary?, tag?, source?, url?, image?}]}. Convention: only fill in image when a reliable image is available; ` + imageURLConvention,
		},
		{
			Name:         "chart",
			Description:  "Line, bar, area, horizontal-bar, scatter, pie, radar, funnel and radial charts. Use when: presenting trends, comparisons, proportions or distributions. Interaction: clicking a legend entry shows or hides its series; hovering shows exact values",
			PropsSummary: `{title?, type:"line|bar|area|horizontal-bar|scatter|pie|radar|funnel|radial", x?:[...], series:[{name, data:[number], type?:"line|bar|area", axis?:"left|right"}], unit?, stacked?, percent?}. Convention: the x of a scatter chart must be numeric; each series can set its own type, so several chart kinds can be mixed in one chart; setting a series' axis to right enables a second Y axis on the right; percent can only be used together with stacked`,
		},
		{
			Name:         "function-plot",
			Description:  "Function graphs and equation curves. Use when: plotting a function or equation, or comparing several curves. Interaction: expressions can be edited, added or removed; drag to pan and zoom the canvas; individual curves can be shown or hidden",
			PropsSummary: `{title?, functions:[{expr}], x?:[min,max], y?:[min,max]}. Convention: expr can be an expression in x, "y = …", "x = …", or an equation containing both x and y; the multiplication sign cannot be omitted and must be written as *; common math functions and the constants pi and e are built in`,
		},
		{
			Name:         "stat-grid",
			Description:  "Key metric cards. Use when: showing a handful of key numbers and how they changed, such as a performance or data overview. Interaction: metrics with history data get a small trend chart, and the statistics period can be switched at any time",
			PropsSummary: `{title?, periods?, items:[{label, value, unit?, delta?, trend?:"up|down|flat", tone?:"positive|negative|neutral", note?, history?:[number]}]}. Convention: trend only decides the arrow direction, while tone decides the good/bad coloring (by default a rise counts as good and a fall as bad; for metrics where lower is better, set tone to positive when the value falls); delta holds only the size of the change; note is at most one sentence; history is ordered from oldest to newest`,
		},
		{
			Name:         "image-gallery",
			Description:  "Image gallery. Use when: there are two or more images to browse or compare together. Interaction: click to enlarge, page left and right, or download",
			PropsSummary: `{title?, images:[{src, alt?, caption?, source?, url?}]}. Convention: ` + imageURLConvention,
		},
		{
			Name:         "gantt",
			Description:  "Task schedule as a Gantt chart. Use when: laying out project plans, milestones and tasks with dependencies. Interaction: hovering shows task details, and each group can be collapsed",
			PropsSummary: `{title?, tasks:[{id?, name, start:"YYYY-MM-DD", end?:"YYYY-MM-DD", days?, group?, progress?:0-100, milestone?, depends?:[id]}]}. Convention: provide either end or days; depends lists the ids of the tasks depended on, and a task without an id uses its name as its id`,
		},
		{
			Name:         "data-table",
			Description:  "Interactive data table. Use when: a table has more than ten rows of data. Interaction: supports sorting by column, keyword search, pagination and CSV export; image columns are shown as thumbnails",
			PropsSummary: `{title?, columns:[{key, label, type?:"string|number|date|boolean|image"}], rows:[{<key>: value}], pageSize?}. Convention: every key of each item in rows must come from the key of a column in columns; ` + imageURLConvention,
		},
		{
			Name:         "calculator",
			Description:  "Parameter calculator. Use when: letting users change parameters themselves and instantly see the estimated result, such as loans, returns or unit conversion. Interaction: drag a slider or type a value and the result refreshes in real time",
			PropsSummary: `{title?, description?, inputs:[{key, label, min, max, step?, default, unit?}], outputs:[{label, expr, unit?, precision?, highlight?}]}. Convention: expr can use addition, subtraction, multiplication, division, ^, parentheses, the keys in inputs, and common math functions`,
		},
		{
			Name:         "decision-tree",
			Description:  "Step-by-step decision tree. Use when: troubleshooting, option selection and issue triage that reach a conclusion through a series of choices. Interaction: click an option to move forward, go back one step, or start over",
			PropsSummary: `{title?, start?, nodes:[{id, text, detail?, options?:[{label, next}]}]}. Convention: a node without options is a conclusion; when start is omitted, the first node is the starting point`,
		},
		{
			Name:         "diff",
			Description:  "Text or code diff. Use when: showing what differs before and after a change. Interaction: switch between the inline view and the side-by-side view; differences are highlighted",
			PropsSummary: `{title?, before, after, language?, beforeLabel?, afterLabel?}`,
		},
		{
			Name:         "quiz",
			Description:  "Multiple-choice and branching Q&A. Use when: running a short quiz, step-by-step Q&A, or a flow that branches on the answers. Interaction: choosing an answer shows correct/incorrect feedback and an explanation; the score is summarized at the end",
			PropsSummary: `{title?, questions:[{id?, question, image?, options:[string|{text, next?, explanation?}], answer?, explanation?, next?}]}. Convention: answer is the index of the correct option, and when omitted the question is only used for branching and is not scored; a next of "end" means the flow ends here; ` + imageURLConvention,
		},
		{
			Name:         switchPanel,
			Description:  "Interactive panel (optional switch). Use when: building small tools where users adjust parameters and watch linked results, combined with other components and controls. Interaction: sliders, dropdowns, toggles, inputs and buttons update each other live",
			PropsSummary: `{component:"panel", state, computed?, children}`,
		},
		{
			Name:         switchWidget,
			Description:  "Embedded interactive illustration (optional switch). Use when: showing what a panel cannot, such as animation, processes over time, 3D, particles or geometry. Interaction: runs in an offline sandbox, can be played and paused, and follows the theme colors",
			PropsSummary: `Use a riochat-widget fence containing a self-contained, independently runnable HTML snippet`,
		},
	}
}
