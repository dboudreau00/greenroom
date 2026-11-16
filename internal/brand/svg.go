package brand

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
)

const (
	svgNS   = "http://www.w3.org/2000/svg"
	xmlNS   = "http://www.w3.org/XML/1998/namespace"
	adobeNS = "http://ns.adobe.com/"
)

// svgElements are the SVG elements a logo may use. Everything that can run
// script, animate an attribute, link out or embed another document (script,
// a, foreignObject, set, animate*, iframe, ...) is left out.
var svgElements = setOf(
	"svg", "g", "defs", "symbol", "use", "path", "rect", "circle", "ellipse",
	"line", "polyline", "polygon", "text", "tspan", "textpath", "title", "desc",
	"metadata", "lineargradient", "radialgradient", "stop", "clippath", "mask",
	"pattern", "marker", "filter", "style", "image", "switch",
	"feblend", "fecolormatrix", "fecomponenttransfer", "fecomposite",
	"feconvolvematrix", "fediffuselighting", "fedisplacementmap",
	"fedistantlight", "fedropshadow", "feflood", "fefunca", "fefuncb",
	"fefuncg", "fefuncr", "fegaussianblur", "feimage", "femerge",
	"femergenode", "femorphology", "feoffset", "fepointlight",
	"fespecularlighting", "fespotlight", "fetile", "feturbulence",
)

// inertNS are editor namespaces whose elements a browser never renders.
var inertNS = setOf(
	"http://www.w3.org/1999/02/22-rdf-syntax-ns#",
	"http://purl.org/dc/elements/1.1/",
	"http://creativecommons.org/ns#",
	"http://sodipodi.sourceforge.net/DTD/sodipodi-0.dtd",
	"http://www.inkscape.org/namespaces/inkscape",
)

var (
	svgDataImageRe = regexp.MustCompile(`(?i)^data:image/(png|jpeg|gif|webp);base64,[a-z0-9+/=\s]*$`)
	cssURLRe       = regexp.MustCompile(`(?i)url\(\s*["']?\s*(.?)`)
)

func setOf(items ...string) map[string]bool {
	m := make(map[string]bool, len(items))
	for _, s := range items {
		m[s] = true
	}
	return m
}

// walkSVG parses the document as XML and checks every token: the root is
// svg in the SVG namespace, elements are on the allowlist or in an inert
// editor namespace, no entity declarations or processing instructions,
// and no attribute that runs script or loads anything from outside.
// Elements and attributes are matched by namespace URI and local name,
// never by prefix.
func walkSVG(b []byte) error {
	d := xml.NewDecoder(bytes.NewReader(bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))))
	d.Strict = true
	depth, roots, inStyle := 0, 0, 0
	for {
		tok, err := d.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("svg refused: not well-formed XML: %v", err)
		}
		switch t := tok.(type) {
		case xml.Directive:
			s := strings.ToUpper(string(t))
			if strings.Contains(s, "ENTITY") || strings.Contains(s, "[") {
				return errors.New("svg refused: DTD internal subset or entity declaration")
			}
		case xml.ProcInst:
			if !strings.EqualFold(t.Target, "xml") {
				return fmt.Errorf("svg refused: processing instruction %s", t.Target)
			}
		case xml.StartElement:
			if depth == 0 {
				roots++
				if roots > 1 || !strings.EqualFold(t.Name.Local, "svg") || (t.Name.Space != svgNS && t.Name.Space != "") {
					return errors.New("svg refused: the root element is not svg in the SVG namespace")
				}
			}
			depth++
			if err := checkSVGElement(t.Name); err != nil {
				return err
			}
			for _, a := range t.Attr {
				if err := checkSVGAttr(a); err != nil {
					return err
				}
			}
			if inStyle > 0 || (isSVGSpace(t.Name.Space) && strings.EqualFold(t.Name.Local, "style")) {
				inStyle++
			}
		case xml.EndElement:
			depth--
			if inStyle > 0 {
				inStyle--
			}
		case xml.CharData:
			if inStyle > 0 {
				if err := checkCSS(string(t), true); err != nil {
					return err
				}
			}
		}
	}
	if roots == 0 {
		return errors.New("svg refused: no svg element")
	}
	return nil
}

func isSVGSpace(ns string) bool { return ns == svgNS || ns == "" }

func checkSVGElement(n xml.Name) error {
	switch {
	case isSVGSpace(n.Space):
		if !svgElements[strings.ToLower(n.Local)] {
			return fmt.Errorf("svg refused: element %s is not allowed", n.Local)
		}
	case inertNS[n.Space], strings.HasPrefix(n.Space, adobeNS):
	default:
		return fmt.Errorf("svg refused: element %s in namespace %s", n.Local, n.Space)
	}
	return nil
}

func checkSVGAttr(a xml.Attr) error {
	if a.Name.Space == "xmlns" || (a.Name.Space == "" && a.Name.Local == "xmlns") {
		return nil
	}
	local := strings.ToLower(a.Name.Local)
	switch {
	case strings.HasPrefix(local, "on"):
		return fmt.Errorf("svg refused: event handler attribute %s", a.Name.Local)
	case local == "base" && (a.Name.Space == xmlNS || a.Name.Space == "xml"):
		return errors.New("svg refused: xml:base attribute")
	case local == "src":
		return errors.New("svg refused: src attribute")
	case local == "href":
		v := strings.TrimSpace(a.Value)
		if !strings.HasPrefix(v, "#") && !svgDataImageRe.MatchString(v) {
			return errors.New("svg refused: href that is not a fragment or an inline raster image")
		}
	}
	if err := checkCSS(a.Value, local == "style"); err != nil {
		return fmt.Errorf("%w (attribute %s)", err, a.Name.Local)
	}
	return nil
}

// checkCSS refuses style text that can load or run anything: url() other
// than a fragment, @import, expression() and javascript:. Every attribute
// value is checked, since presentation attributes (fill, filter, mask)
// take url() too. In a style attribute or element a backslash is refused
// as well, since a CSS escape can spell any of these.
func checkCSS(s string, style bool) error {
	l := strings.ToLower(s)
	for _, bad := range []string{"@import", "expression(", "javascript:"} {
		if strings.Contains(l, bad) {
			return fmt.Errorf("svg refused: style contains %s", bad)
		}
	}
	if style && strings.Contains(l, `\`) {
		return errors.New("svg refused: CSS escape in style")
	}
	for _, m := range cssURLRe.FindAllStringSubmatch(l, -1) {
		if m[1] != "#" {
			return errors.New("svg refused: url() that is not a fragment")
		}
	}
	return nil
}
