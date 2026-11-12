package adminx

// FieldNames lists the setting names of a settings sub-page, without the action buttons and text rows.
func FieldNames(fs []Field) []string {
	var out []string
	for _, f := range fs {
		if f.Type == "watest" || f.Type == "dsnow" || f.Type == "formbtn" || f.Type == "test" || f.Type == "note" || f.Type == "pre" {
			continue // action button or text, not a setting
		}
		out = append(out, f.Name)
	}
	return out
}
