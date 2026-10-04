package report

import (
	"html/template"
	"strings"

	"github.com/danielgavin-code/OrderEcho/internal/cert"
)

// reportJS is the only script: expand/collapse all, and open every case
// before printing. The report reads fine without it.
const reportJS = `(function () {
  function all(open) { document.querySelectorAll('details.case').forEach(function (d) { d.open = open; }); }
  var e = document.getElementById('expand-all'), c = document.getElementById('collapse-all');
  if (e) e.addEventListener('click', function () { all(true); });
  if (c) c.addEventListener('click', function () { all(false); });
  window.addEventListener('beforeprint', function () { all(true); });
})();`

var funcs = template.FuncMap{
	"dash":  dash,
	"badge": BadgeClass,
	"join":  strings.Join,
	"inc":   func(i int) int { return i + 1 },
	"stepbadge": func(s string) string {
		if s == cert.StepSkipped {
			return "NA"
		}
		return BadgeClass(s)
	},
}

var page = template.Must(template.New("report").Funcs(funcs).Parse(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Res.Title}} — {{.Verdict}} — run {{.Res.RunID}}</title>
<style>
{{.CSS}}
</style>
</head>
<body>
<main class="report">

<header class="top">
<div class="tools noprint"><button type="button" id="expand-all">Expand all</button> <button type="button" id="collapse-all">Collapse all</button></div>
<p class="muted small">OrderEcho certification report</p>
<h1>{{.Res.Title}}</h1>
<div class="card">
<dl class="kv">
<dt>Suite</dt><dd>{{.Res.Suite}} ({{dash .Res.SuiteFile}})</dd>
<dt>Target</dt><dd>{{.Res.Target}} ({{dash .Res.TargetFile}})</dd>
<dt>Counterparty</dt><dd>{{dash .Res.Counterparty}}</dd>
<dt>Session</dt><dd>{{.Res.Session}} — {{.Res.FixVersion}}, {{dash .Res.SenderCompID}} → {{dash .Res.TargetCompID}} @ {{dash .Res.Address}}</dd>
<dt>Run</dt><dd class="mono">{{.Res.RunID}}</dd>
<dt>Start (UTC)</dt><dd>{{.Res.Start}}</dd>
<dt>End (UTC)</dt><dd>{{.Res.End}}</dd>
<dt>Agent</dt><dd>OrderEcho {{.Res.Version}} (build {{.Res.Build}})</dd>
<dt>Exit code</dt><dd>{{.Res.Exit}}</dd>
{{if .Res.SessionErr}}<dt>Session</dt><dd>{{.Res.SessionErr}}</dd>{{end}}
{{if .Res.RunError}}<dt>Run error</dt><dd>{{.Res.RunError}}</dd>{{end}}
</dl>
</div>
</header>

<section class="first" id="verdict">
<div class="verdict {{.VerdictClass}}">
<div class="word">{{.Verdict}}</div>
<p>{{.VerdictReason}}.</p>
</div>
<div class="table-wrap">
<table>
<thead><tr><th></th>{{range $i, $s := .Statuses}}<th class="num"><span class="badge {{index $.StatusClasses $i}}">{{$s}}</span></th>{{end}}</tr></thead>
<tbody>
<tr><td>All cases</td>{{range .AllCounts}}<td class="num">{{.}}</td>{{end}}</tr>
<tr><td>Required cases</td>{{range .RequiredCounts}}<td class="num">{{.}}</td>{{end}}</tr>
</tbody>
</table>
</div>
<p class="small muted">CERTIFIED: every required case of the suite is PASS or N/A. NOT CERTIFIED: a required case FAILED. INCOMPLETE: no required FAIL, but required cases are pending, blocked, in error or not run.</p>
</section>

<section class="first" id="sections">
<h2>Summary by section</h2>
<div class="table-wrap">
<table>
<thead><tr><th>Section</th><th>Name</th>{{range $i, $s := .Statuses}}<th class="num">{{$s}}</th>{{end}}<th class="num">Total</th></tr></thead>
<tbody>
{{range .Sections}}<tr><td class="mono">{{.ID}}</td><td>{{.Name}}</td>{{range .Counts}}<td class="num">{{if .}}{{.}}{{else}}<span class="muted">·</span>{{end}}</td>{{end}}<td class="num">{{.Total}}</td></tr>
{{end}}</tbody>
</table>
</div>
</section>

<section id="cases">
<h2>Cases</h2>
<p class="small muted">Each case expands to its steps, the messages that satisfied its expectations, the order checks, any attestation, and its FIX evidence (only this case's window of the session log).</p>
<div class="report-cases">
{{range .Cases}}<details class="case" id="case-{{.ID}}">
<summary><span class="mono">{{.ID}}</span><span>{{.Title}}</span><span class="small">{{if .Required}}req{{else}}opt{{end}}</span><span class="small">{{.Mode}}</span><span><span class="badge {{.Badge}}">{{.Status}}</span></span><span class="small">{{.Reason}}</span></summary>
<div class="case-body">
<p class="small"><strong>Task:</strong> {{.Task}}{{if .Start}} <span class="muted">· {{.Start}} → {{.End}}</span>{{end}}</p>
{{if .Warnings}}<div class="notice warn small">{{range .Warnings}}<div>{{.}}</div>{{end}}</div>{{end}}
<h4>Steps</h4>
{{if .Steps}}<div class="table-wrap"><table class="small">
<thead><tr><th>#</th><th>Type</th><th>Time (UTC)</th><th>Outcome</th><th>Detail</th></tr></thead>
<tbody>{{range $i, $st := .Steps}}<tr><td class="num">{{inc $i}}</td><td>{{$st.Type}}</td><td class="mono tiny ts">{{$st.TS}}</td><td><span class="badge {{stepbadge $st.Status}}">{{$st.Status}}</span></td><td class="mono tiny">{{$st.Detail}}</td></tr>{{end}}</tbody>
</table></div>{{else}}<p class="small muted">No steps recorded.</p>{{end}}
{{if .Expectations}}<h4>Expectations matched</h4>
<ul class="small mono">{{range .Expectations}}<li>{{.}}</li>{{end}}</ul>{{end}}
{{if .Checks}}<h4>Order checks</h4>
{{range .Checks}}<p class="small"><strong>{{.Ref}}</strong> <span class="mono">{{.ClOrdID}}</span> — verdict <span class="badge {{badge .Verdict}}">{{.Verdict}}</span></p>
<div class="table-wrap"><table class="small"><thead><tr><th>Check</th><th>Status</th><th>Explanation</th></tr></thead>
<tbody>{{range .Checks}}<tr><td class="mono">{{.Name}}</td><td><span class="badge {{badge .Status}}">{{.Status}}</span></td><td>{{.Explanation}}</td></tr>{{end}}</tbody></table></div>
{{end}}{{end}}
{{if .Attestation}}<h4>Attestation</h4>
<dl class="kv small"><dt>Status</dt><dd>{{.Attestation.Status}}</dd><dt>By</dt><dd>{{.Attestation.By}}</dd><dt>When</dt><dd>{{dash .Attestation.At}}</dd><dt>Note</dt><dd>{{dash .Attestation.Note}}</dd><dt>Source</dt><dd>{{dash .Attestation.File}}</dd></dl>{{end}}
<h4>FIX evidence</h4>
{{if .Evidence}}<div class="table-wrap"><table class="small">
<thead><tr><th>Time (UTC)</th><th>Dir</th><th>Seq</th><th>Message</th><th></th></tr></thead>
<tbody>{{range .Evidence}}<tr><td class="mono tiny ts">{{.Time}}</td><td><span class="badge {{.Dir}}">{{.Dir}}</span></td><td class="num mono">{{.Seq}}</td><td class="mono tiny">{{.Line}}</td><td>{{range .Flags}}<span class="badge {{.}}">{{.}}</span> {{end}}</td></tr>{{end}}</tbody>
</table></div>
<pre class="raw">{{range .Raw}}{{.}}
{{end}}</pre>
<p class="tiny muted">{{.ID}}/fix.log: {{len .Raw}} line(s); {{.ID}}/evidence.jsonl: {{.Events}} record(s).</p>{{else}}<p class="small muted">{{.EvidenceNote}}.</p>{{end}}
</div>
</details>
{{end}}</div>
</section>

<section id="deviations">
<h2>Deviations</h2>
{{with .Res.Deviations}}<p class="small muted">Drafted by the runner from this run's N/A reasons and warnings (case 9.2); a human reviews and attests it.</p>
<h3>Not applicable</h3>
{{if .NotApplicable}}<ul>{{range .NotApplicable}}<li>{{.Text}} — case(s) {{join .Cases ", "}}</li>{{end}}</ul>{{else}}<p class="muted">None.</p>{{end}}
<h3>Warnings</h3>
{{if .Warnings}}<ul>{{range .Warnings}}<li class="mono small">{{.Text}} — case(s) {{join .Cases ", "}}</li>{{end}}</ul>{{else}}<p class="muted">None.</p>{{end}}
{{else}}<p class="muted">This run did not include the deviations review (case 9.2).</p>{{end}}
<h3>N/A cases</h3>
{{if .NA}}<div class="table-wrap"><table class="small"><thead><tr><th>Case</th><th>Title</th><th>Reason</th></tr></thead>
<tbody>{{range .NA}}<tr><td class="mono"><a href="#case-{{.ID}}">{{.ID}}</a></td><td>{{.Title}}</td><td>{{.Reason}}</td></tr>{{end}}</tbody></table></div>{{else}}<p class="muted">No case is N/A.</p>{{end}}
</section>

<section id="attestations">
<h2>Attestations</h2>
{{if .Attested}}<div class="table-wrap"><table class="small"><thead><tr><th>Case</th><th>Status</th><th>By</th><th>When (UTC)</th><th>Note</th><th>Source</th><th>Case result</th></tr></thead>
<tbody>{{range .Attested}}<tr><td class="mono"><a href="#case-{{.ID}}">{{.ID}}</a></td><td>{{.Attestation.Status}}</td><td>{{.Attestation.By}}</td><td class="mono tiny ts">{{dash .Attestation.At}}</td><td>{{dash .Attestation.Note}}</td><td class="tiny">{{dash .Attestation.File}}</td><td><span class="badge {{.Badge}}">{{.Status}}</span></td></tr>{{end}}</tbody></table></div>
{{else}}<p class="muted">No human attestations in this run.</p>{{end}}
</section>

<section id="integrity">
<h2>Integrity appendix</h2>
{{if .Sealed}}<p class="small">SHA-256 digests recorded at run end. <span class="mono">results.json</span>'s own digest is kept in <span class="mono">results.sha256</span>; every other digest is inside <span class="mono">results.json</span>. To check them all: <code>orderecho cert verify {{.Res.RunID}}</code> (exit 0: intact, 9: tampered).</p>
<div class="table-wrap"><table class="small"><thead><tr><th>File</th><th>SHA-256</th></tr></thead>
<tbody><tr><td class="mono">results.json</td><td class="hash">{{dash .ResultsDigest}}</td></tr>
{{range .Hashes}}<tr><td class="mono">{{.Path}}</td><td class="hash">{{.SHA256}}</td></tr>
{{end}}</tbody></table></div>
{{else}}<p class="notice warn small">This run was written before OrderEcho 0.5.0 and has no integrity record; its files cannot be verified.</p>{{end}}
</section>

<footer>Generated from results.json and the per-case evidence by OrderEcho (report format 1). Statuses, reasons and checks are the runner's; nothing in this report is judged by hand except where an attestation says so.</footer>
</main>
<script>
{{.JS}}
</script>
</body>
</html>
`))
