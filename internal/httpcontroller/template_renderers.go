package httpcontroller

import (
	"bytes"
	"fmt"
	"html/template"
	"io"
	"log"
	"log/slog"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
)

// LocaleData represents a locale with its code and full name.
type LocaleData struct {
	Code string
	Name string
}

// ProviderOption represents an option for the image provider select field.
type ProviderOption struct {
	Value   string
	Display string
}

// PageData represents data for rendering a page.
type PageData struct {
	C               echo.Context   // The Echo context for the current request
	Page            string         // The name or identifier of the current page
	Title           string         // The title of the page
	Settings        *conf.Settings // Application settings
	Locales         []LocaleData   // List of available locales
	Charts          template.HTML  // HTML content for charts, if any
	PreloadFragment string         // The preload route for the current page
}

// TemplateRenderer is a custom HTML template renderer for Echo framework.
type TemplateRenderer struct {
	templates *template.Template
	logger    *slog.Logger
}

// validateErrorTemplates checks if all required error templates exist
func (t *TemplateRenderer) validateErrorTemplates() error {
	requiredTemplates := []string{"error-404", "error-500", "error-default"}
	for _, name := range requiredTemplates {
		if tmpl := t.templates.Lookup(name); tmpl == nil {
			return errors.Newf("required error template not found: %s", name).
				Component("template_renderer").
				Category(errors.CategoryConfiguration).
				Context("operation", "validate_error_templates").
				Context("template_name", name).
				Build()
		}
	}
	return nil
}

// Render renders a template with the given data.
func (t *TemplateRenderer) Render(w io.Writer, name string, data interface{}, c echo.Context) error {
	// Create a buffer to capture any template execution errors
	var buf bytes.Buffer
	err := t.templates.ExecuteTemplate(&buf, name, data)
	if err != nil {
		if t.logger != nil {
			t.logger.Error("Error executing template", "template_name", name, "error", err)
		} else {
			log.Printf("ERROR (TemplateRenderer): Error executing template %s: %v", name, err)
		}
		// Wrap the error with enhanced error handling
		return errors.New(err).
			Component("template_renderer").
			Category(errors.CategoryConfiguration).
			Context("operation", "execute_template").
			Context("template_name", name).
			Context("error_detail", err.Error()).
			Build()
	}

	// If execution was successful, write the result to the original writer
	_, err = buf.WriteTo(w)
	if err != nil {
		if t.logger != nil {
			t.logger.Error("Error writing template result", "template_name", name, "error", err)
		} else {
			log.Printf("ERROR (TemplateRenderer): Error writing template result for %s: %v", name, err)
		}
		// Wrap the error with enhanced error handling
		return errors.New(err).
			Component("template_renderer").
			Category(errors.CategoryFileIO).
			Context("operation", "write_template_result").
			Context("template_name", name).
			Build()
	}
	return nil
}

// setupTemplateRenderer configures the template renderer for the server
func (s *Server) setupTemplateRenderer() {
	// Get the template functions
	funcMap := s.GetTemplateFunctions()

	// Parse all templates from the ViewsFs
	tmpl, err := template.New("").Funcs(funcMap).ParseFS(ViewsFs, "views/*/*.html", "views/*/*/*.html")
	if err != nil {
		log.Fatalf("Failed to parse templates: %v", err)
	}

	// Create the renderer, passing the structured logger
	renderer := &TemplateRenderer{
		templates: tmpl,
		logger:    s.webLogger,
	}

	// Validate that all required error templates exist
	if err := renderer.validateErrorTemplates(); err != nil {
		log.Fatalf("Template validation failed: %v", err)
	}

	// Set the custom renderer
	s.Echo.Renderer = renderer
}

// RenderContent renders the content template with the given data
func (s *Server) RenderContent(data interface{}) (template.HTML, error) {
	// Assert that the data is of the expected type
	d, ok := data.(RenderData)
	if !ok {
		// Return an error if the data type is invalid
		return "", errors.Newf("invalid data type for RenderContent: %T", data).
			Component("template_renderer").
			Category(errors.CategoryValidation).
			Context("operation", "render_content").
			Context("data_type", fmt.Sprintf("%T", data)).
			Build()
	}

	// Extract the context from the data
	c := d.C

	// Get the current path from the context
	path := c.Path()

	// Look up the route for the current path
	_, isPageRoute := s.pageRoutes[path]
	_, isFragment := s.partialRoutes[path]

	// Is a login route, set isLoginRoute to true
	isLoginRoute := path == "/login"

	if !isPageRoute && !isFragment && !isLoginRoute {
		// Return an error if no route is found for the path
		return "", errors.Newf("no route found for path: %s", path).
			Component("template_renderer").
			Category(errors.CategoryValidation).
			Context("operation", "find_route").
			Context("path", path).
			Build()
	}

	// Create a buffer to store the rendered template
	buf := new(bytes.Buffer)

	// Render the template using the Echo renderer
	err := s.Echo.Renderer.Render(buf, d.Page, d, c)
	if err != nil {
		// Return an error if template rendering fails
		return "", errors.New(err).
			Component("template_renderer").
			Category(errors.CategoryConfiguration).
			Context("operation", "render_page_content").
			Context("page", d.Page).
			Context("path", path).
			Build()
	}

	// Return the rendered template as HTML
	return template.HTML(buf.String()), nil
}

// renderSettingsContent returns the appropriate content template for a given settings page
func (s *Server) renderSettingsContent(c echo.Context) (template.HTML, error) {
	// Extract the settings type from the path
	path := c.Path()
	settingsType := strings.TrimPrefix(path, "/settings/")
	templateName := fmt.Sprintf("%sSettings", settingsType)

	// DEBUG do we have CSRF token?
	csrfToken := c.Get(CSRFContextKey)
	if csrfToken == nil {
		log.Printf("Warning: 🚨 CSRF token not found in context for settings page: %s", path)
		csrfToken = ""
	} else {
		log.Printf("Debug: ✅ CSRF token found in context for settings page: %s", path)
	}

	// Prepare image provider options for the template
	providerOptionList := []ProviderOption{
		{Value: "auto", Display: "Auto (Default)"}, // Always add auto first
	}

	multipleProvidersAvailable := false
	providerCount := 0
	if s.Handlers.BirdImageCache != nil {
		if registry := s.Handlers.BirdImageCache.GetRegistry(); registry != nil {
			registry.RangeProviders(func(name string, cache *imageprovider.BirdImageCache) bool {
				// Simple capitalization for display name (Rune-aware)
				var displayName string
				if name != "" {
					r, size := utf8.DecodeRuneInString(name)
					displayName = strings.ToUpper(string(r)) + name[size:]
				} else {
					displayName = "(unknown)"
				}
				providerOptionList = append(providerOptionList, ProviderOption{Value: name, Display: displayName})
				providerCount++
				return true // Continue ranging
			})
			multipleProvidersAvailable = providerCount > 1 // Considered multiple only if more than one actual provider exists

			// Sort the providers alphabetically by display name (excluding the first 'auto' entry)
			if len(providerOptionList) > 2 { // Need at least 3 elements to sort the part after 'auto'
				sub := providerOptionList[1:] // Create a sub-slice for sorting
				sort.Slice(sub, func(i, j int) bool {
					return sub[i].Display < sub[j].Display // Compare elements within the sub-slice
				})
			}
		}
	} else {
		log.Println("Warning: ImageProviderRegistry is nil, cannot get provider names.")
	}

	// Prepare the data for the template
	data := map[string]interface{}{
		"Settings":                   s.Settings,             // Application settings
		"Locales":                    s.prepareLocalesData(), // Prepare locales data for the UI
		"EqFilterConfig":             conf.EqFilterConfig,    // Equalizer filter configuration for the UI
		"TemplateName":               templateName,
		"CSRFToken":                  csrfToken,
		"ProviderOptionList":         providerOptionList,         // Use the sorted list of structs
		"MultipleProvidersAvailable": multipleProvidersAvailable, // Add flag
	}

	// DEBUG Log the species settings
	//log.Printf("Species Settings: %+v", s.Settings.Realtime.Species)

	if templateName == "speciesSettings" {
		log.Printf("Debug: Species Config being passed to template: %+v", s.Settings.Realtime.Species.Config)
	}

	// Render the template
	var buf bytes.Buffer
	err := s.Echo.Renderer.Render(&buf, templateName, data, c)

	// Handle rendering errors
	if err != nil {
		log.Printf("ERROR: Failed to render settings content: %v", err)
		// Log the template data that caused the error
		log.Printf("ERROR: Template data dump: %+v", data)
		return "", errors.New(err).
			Component("template_renderer").
			Category(errors.CategoryConfiguration).
			Context("operation", "render_settings_content").
			Context("template_name", templateName).
			Context("settings_type", settingsType).
			Context("path", path).
			Build()
	}

	// Return the rendered HTML
	return template.HTML(buf.String()), nil
}
