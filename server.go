package main

import (
	"encoding/json"
	"fmt"
	"html"
	"log"
	"net/http"
	"strings"
)

func startServer(port int) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", serveIndex)
	mux.HandleFunc("/api/tree", serveTree)
	mux.HandleFunc("/api/messages", serveMessages)
	mux.HandleFunc("/api/content", serveContent)
	mux.HandleFunc("/api/export", serveExport)

	addr := fmt.Sprintf(":%d", port)
	fmt.Printf("\n  Claude Log Viewer\n  http://localhost%s\n\n", addr)
	return http.ListenAndServe(addr, mux)
}

func serveTree(w http.ResponseWriter, r *http.Request) {
	tree, err := loadTree()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(tree)
}

type messageView struct {
	Type      string     `json:"type"`
	Timestamp string     `json:"timestamp"`
	Model     string     `json:"model,omitempty"`
	HTML      string     `json:"html,omitempty"`
	Thinking  string     `json:"thinking,omitempty"`
	Tools     []string   `json:"tools,omitempty"`
	Tokens    *tokenView `json:"tokens,omitempty"`
}
type tokenView struct {
	In         int `json:"in"`
	Out        int `json:"out"`
	CacheRead  int `json:"cacheRead,omitempty"`
	CacheWrite int `json:"cacheWrite,omitempty"`
}

func serveMessages(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		http.Error(w, "missing path", 400)
		return
	}
	entries, err := parseConversation(path)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	var messages []messageView
	for _, entry := range entries {
		switch entry.Type {
		case "user":
			if entry.Parsed == nil {
				continue
			}
			blocks := getContentBlocks(entry.Parsed)
			isToolResult := false
			for _, b := range blocks {
				if b.Type == "tool_result" {
					isToolResult = true
					break
				}
			}
			if isToolResult {
				continue
			}
			var buf strings.Builder
			for _, b := range blocks {
				if b.Type == "text" && b.Text != "" {
					buf.WriteString(markdownToHTML(b.Text))
				}
			}
			messages = append(messages, messageView{Type: "user", Timestamp: formatTimestampFull(entry.Timestamp), HTML: buf.String()})
		case "assistant":
			if entry.Parsed == nil {
				continue
			}
			blocks := getContentBlocks(entry.Parsed)
			mv := messageView{Type: "assistant", Timestamp: formatTimestampFull(entry.Timestamp), Model: entry.Parsed.Model}
			var buf strings.Builder
			for _, b := range blocks {
				switch b.Type {
				case "thinking":
					if b.Thinking != "" {
						mv.Thinking = b.Thinking
					}
				case "text":
					if b.Text != "" {
						buf.WriteString(markdownToHTML(b.Text))
					}
				case "tool_use":
					mv.Tools = append(mv.Tools, formatToolUse(b.Name, b.Input))
				}
			}
			mv.HTML = buf.String()
			if entry.Parsed.Usage != nil {
				mv.Tokens = &tokenView{In: entry.Parsed.Usage.InputTokens, Out: entry.Parsed.Usage.OutputTokens, CacheRead: entry.Parsed.Usage.CacheReadInputTokens, CacheWrite: entry.Parsed.Usage.CacheCreationInputTokens}
			}
			messages = append(messages, mv)
		case "system":
			if entry.Subtype == "local_command" {
				messages = append(messages, messageView{Type: "system", Timestamp: formatTimestampFull(entry.Timestamp), HTML: html.EscapeString(extractCommandName(entry.Content))})
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(messages)
}

func serveContent(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		http.Error(w, "missing path", 400)
		return
	}
	content, err := readFileContent(path)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"raw": content, "html": markdownToHTML(content)})
}

func serveExport(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		http.Error(w, "missing path", 400)
		return
	}
	entries, err := parseConversation(path)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename=conversation.html")
	if err := exportHTMLTo(entries, w, path); err != nil {
		log.Printf("export error: %v", err)
	}
}

func extractCommandName(content string) string {
	if idx := strings.Index(content, "<command-name>"); idx >= 0 {
		start := idx + len("<command-name>")
		if end := strings.Index(content[start:], "</command-name>"); end >= 0 {
			return content[start : start+end]
		}
	}
	return content
}

func serveIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, indexHTML)
}

const indexHTML = `<!DOCTYPE html>
<html lang="en"><head>
<meta charset="UTF-8"><meta name="viewport" content="width=device-width,initial-scale=1.0">
<title>Claude Log Viewer</title>
<link rel="stylesheet" href="https://cdnjs.cloudflare.com/ajax/libs/highlight.js/11.11.1/styles/github-dark.min.css">
<script src="https://cdnjs.cloudflare.com/ajax/libs/highlight.js/11.11.1/highlight.min.js"></script>
<style>
*,*::before,*::after{box-sizing:border-box;margin:0;padding:0}
:root{--bg:#FAF9F7;--card-bg:#fff;--sidebar-bg:#F5F2EB;--sidebar-hover:#EBE8E0;--text:#1C1917;--text-secondary:#57534E;--text-muted:#A8A29E;--accent:#D97706;--accent-light:#FEF3C7;--accent-dim:#92400E;--user-accent:#6366F1;--user-bg:#EEF2FF;--border:#E7E5E0;--code-bg:#F5F3EF;--shadow:0 1px 3px rgba(0,0,0,.06);--shadow-md:0 4px 8px rgba(0,0,0,.08)}
html,body{height:100%;overflow:hidden}
body{font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Roboto,sans-serif;background:var(--bg);color:var(--text);-webkit-font-smoothing:antialiased}

/* ── Project List ── */
.screen{display:none;height:100vh;flex-direction:column}
.screen.active{display:flex}
.topbar{padding:14px 24px;border-bottom:1px solid var(--border);background:var(--card-bg);display:flex;align-items:center;gap:10px}
.topbar h1{font-size:.95em;font-weight:700;color:var(--accent);display:flex;align-items:center;gap:8px}
.logo{width:20px;height:20px;background:var(--accent);border-radius:5px;display:inline-flex;align-items:center;justify-content:center;color:#fff;font-size:10px;font-weight:800}
.topbar .stats{margin-left:auto;font-size:.75em;color:var(--text-muted)}
.project-list{flex:1;overflow-y:auto;padding:24px;max-width:900px;margin:0 auto;width:100%}
.project-card{background:var(--card-bg);border:1px solid var(--border);border-radius:10px;padding:16px 20px;margin-bottom:12px;cursor:pointer;transition:all .15s}
.project-card:hover{border-color:var(--accent);box-shadow:var(--shadow-md)}
.pc-name{font-size:.9em;font-weight:700;color:var(--text);margin-bottom:4px}
.pc-meta{font-size:.76em;color:var(--text-muted);display:flex;gap:10px;flex-wrap:wrap}
.pc-badge{font-size:.7em;padding:2px 7px;border-radius:4px;background:var(--accent-light);color:var(--accent-dim);font-weight:500}
.pc-convs{margin-top:10px;border-top:1px solid var(--border);padding-top:8px}
.pc-conv{font-size:.8em;color:var(--text-secondary);padding:3px 0;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}

/* ── Project Detail ── */
.detail-layout{display:flex;flex:1;overflow:hidden}
.sidebar{width:370px;min-width:300px;background:var(--sidebar-bg);border-right:1px solid var(--border);display:flex;flex-direction:column}
.sb-header{padding:10px 14px;border-bottom:1px solid var(--border)}
.sb-back{font-size:.78em;color:var(--user-accent);cursor:pointer;display:flex;align-items:center;gap:4px;margin-bottom:6px}
.sb-back:hover{text-decoration:underline}
.sb-projname{font-size:.82em;font-weight:700;color:var(--text)}
.sb-badges{margin-top:6px;display:flex;gap:4px;flex-wrap:wrap}
.sb-badge{font-size:.68em;padding:2px 7px;border-radius:4px;background:var(--border);color:var(--text-secondary);cursor:pointer}
.sb-badge:hover{background:var(--accent-light);color:var(--accent-dim)}
.sb-list{flex:1;overflow-y:auto;padding:6px 0}
.sb-list::-webkit-scrollbar{width:5px}
.sb-list::-webkit-scrollbar-thumb{background:var(--border);border-radius:3px}
.sb-conv{padding:10px 14px;cursor:pointer;border-left:3px solid transparent;transition:all .1s}
.sb-conv:hover{background:var(--sidebar-hover)}
.sb-conv.selected{background:var(--card-bg);border-left-color:var(--accent);box-shadow:var(--shadow)}
.sb-conv-title{font-size:.82em;font-weight:600;color:var(--text);display:-webkit-box;-webkit-line-clamp:2;-webkit-box-orient:vertical;overflow:hidden;line-height:1.4}
.sb-conv-meta{font-size:.68em;color:var(--text-muted);margin-top:3px;display:flex;gap:8px}
.sb-agent{font-size:.72em;color:var(--text-muted);padding:3px 14px 3px 24px;cursor:pointer;display:flex;gap:4px}
.sb-agent:hover{color:var(--text-secondary);background:var(--sidebar-hover)}
.sb-agent .at{color:var(--accent);font-weight:600;flex-shrink:0}

.content-area{flex:1;display:flex;flex-direction:column;overflow:hidden;background:var(--card-bg)}
.ct-header{padding:12px 24px;border-bottom:1px solid var(--border);display:flex;align-items:center;justify-content:space-between}
.ct-title{font-size:.88em;font-weight:600}
.ct-sub{font-size:.74em;color:var(--text-secondary);margin-top:2px}
.export-btn{padding:6px 14px;border:1px solid var(--border);border-radius:6px;background:var(--card-bg);color:var(--text);font-size:.76em;cursor:pointer;font-weight:500}
.export-btn:hover{background:var(--accent);color:#fff;border-color:var(--accent)}
.ct-body{flex:1;overflow-y:auto}
.ct-body::-webkit-scrollbar{width:7px}
.ct-body::-webkit-scrollbar-thumb{background:#ddd;border-radius:4px}
.ct-empty{display:flex;align-items:center;justify-content:center;height:100%;color:var(--text-muted);font-size:.9em}

/* Messages */
.messages{max-width:820px;margin:0 auto;padding:24px 28px}
.message{margin-bottom:24px}
.msg-header{display:flex;align-items:center;gap:9px;margin-bottom:7px}
.avatar{width:28px;height:28px;border-radius:50%;display:flex;align-items:center;justify-content:center;font-size:.7em;font-weight:700;flex-shrink:0}
.avatar.u{background:var(--user-bg);color:var(--user-accent)}
.avatar.a{background:var(--accent-light);color:var(--accent-dim)}
.msg-role{font-size:.8em;font-weight:600}
.msg-model{font-size:.7em;color:var(--text-muted)}
.msg-time{font-size:.7em;color:var(--text-muted);margin-left:auto}
.msg-body{padding-left:37px;line-height:1.7;font-size:.91em}
.msg-body p{margin:7px 0}.msg-body p:first-child{margin-top:0}
.msg-body pre,.md-viewer pre{background:#0d1117;color:#e6edf3;padding:14px 16px;border-radius:8px;overflow-x:auto;font-size:.85em;line-height:1.55;margin:14px 0;border:1px solid #30363d}
.msg-body code,.md-viewer code{font-family:'JetBrains Mono','Fira Code','SF Mono',monospace}
.msg-body p code,.msg-body li code,.md-viewer p code,.md-viewer li code{background:var(--code-bg);padding:2px 5px;border-radius:4px;font-size:.87em;color:var(--accent-dim)}
.msg-body h2,.msg-body h3,.msg-body h4,.md-viewer h2,.md-viewer h3,.md-viewer h4{margin:18px 0 8px;font-weight:600}
.msg-body ul,.msg-body ol,.md-viewer ul,.md-viewer ol{padding-left:22px;margin:7px 0}
.msg-body li,.md-viewer li{margin:3px 0}
.msg-body table,.md-viewer table{border-collapse:collapse;width:100%;margin:14px 0;font-size:.88em}
.msg-body th,.msg-body td,.md-viewer th,.md-viewer td{border:1px solid var(--border);padding:7px 12px;text-align:left}
.msg-body th,.md-viewer th{background:var(--code-bg);font-weight:600;font-size:.85em}
.msg-body blockquote,.md-viewer blockquote{border-left:3px solid var(--accent);padding:8px 16px;margin:12px 0;background:var(--accent-light);color:var(--text-secondary);border-radius:0 6px 6px 0}
.tool-call{display:flex;align-items:center;gap:7px;padding:6px 10px;background:#FFFBEB;border:1px solid #FDE68A;border-radius:6px;margin:6px 0;font-family:monospace;font-size:.76em;color:#92400E}
.tool-icon{font-weight:700;color:var(--accent)}
.thinking-block{margin:9px 0;border:1px solid var(--border);border-radius:7px;overflow:hidden}
.thinking-toggle{padding:7px 12px;background:var(--code-bg);cursor:pointer;font-size:.76em;color:var(--text-secondary);display:flex;align-items:center;gap:5px;user-select:none}
.thinking-toggle:hover{background:var(--sidebar-hover)}
.tg-arrow{transition:transform .15s;display:inline-block;font-size:.8em}
.tg-arrow.open{transform:rotate(90deg)}
.thinking-content{display:none;padding:12px;font-size:.78em;color:var(--text-secondary);font-style:italic;white-space:pre-wrap;max-height:400px;overflow-y:auto;line-height:1.6;background:#FAFAF8;border-top:1px solid var(--border)}
.thinking-content.open{display:block}
.token-info{font-size:.65em;color:var(--text-muted);font-family:monospace;padding-left:37px;opacity:0;transition:opacity .2s;height:0;overflow:hidden}
.message:hover .token-info{opacity:1;height:auto;margin-top:4px}
.system-msg{padding:5px 10px 5px 37px;font-size:.74em;color:var(--text-muted);font-style:italic}
.md-viewer{max-width:820px;margin:0 auto;padding:28px;line-height:1.75;font-size:.92em}
.md-viewer p{margin:8px 0}
.loading-wrap{display:flex;align-items:center;justify-content:center;height:100%;color:var(--text-muted);font-size:.88em}
@keyframes spin{to{transform:rotate(360deg)}}
.spinner{width:18px;height:18px;border:2px solid var(--border);border-top-color:var(--accent);border-radius:50%;animation:spin .7s linear infinite;margin-right:8px}
</style></head>
<body>

<!-- Screen 1: Project List -->
<div class="screen active" id="screen-projects">
  <div class="topbar">
    <h1><span class="logo">C</span> Claude Log Viewer</h1>
    <span class="stats" id="global-stats"></span>
  </div>
  <div class="project-list" id="project-list">
    <div class="loading-wrap"><div class="spinner"></div> Loading projects...</div>
  </div>
</div>

<!-- Screen 2: Project Detail -->
<div class="screen" id="screen-detail">
  <div class="topbar">
    <h1><span class="logo">C</span> Claude Log Viewer</h1>
  </div>
  <div class="detail-layout">
    <aside class="sidebar">
      <div class="sb-header">
        <div class="sb-back" id="back-btn">&#9664; All Projects</div>
        <div class="sb-projname" id="sb-projname"></div>
        <div class="sb-badges" id="sb-badges"></div>
      </div>
      <div class="sb-list" id="sb-list"></div>
    </aside>
    <main class="content-area">
      <div class="ct-header" id="ct-header" style="display:none">
        <div><div class="ct-title" id="ct-title"></div><div class="ct-sub" id="ct-sub"></div></div>
        <button class="export-btn" id="export-btn" style="display:none">Export HTML</button>
      </div>
      <div class="ct-body" id="ct-body">
        <div class="ct-empty">Select a conversation</div>
      </div>
    </main>
  </div>
</div>

<script>
(function(){
  var treeData=null, selectedPath=null, currentProjIdx=-1;

  function esc(s){var d=document.createElement('div');d.textContent=s||'';return d.innerHTML}
  function fmtDate(iso){try{var d=new Date(iso);return d.toLocaleDateString(undefined,{month:'short',day:'numeric'})+' '+d.toLocaleTimeString(undefined,{hour:'2-digit',minute:'2-digit'})}catch(e){return iso||''}}
  function wordCount(s){return s?s.split(/\s+/).filter(function(w){return w.length>0}).length:0}

  function showScreen(id){
    document.querySelectorAll('.screen').forEach(function(el){el.classList.remove('active')});
    document.getElementById(id).classList.add('active');
  }

  // Load tree data
  fetch('/api/tree').then(function(r){return r.json()}).then(function(data){
    treeData=data;
    document.getElementById('global-stats').textContent=
      data.stats.totalProjects+' projects | '+data.stats.totalConversations+' conversations | '+data.stats.totalMessages+' messages';
    renderProjectList();
  }).catch(function(e){
    document.getElementById('project-list').innerHTML='<div style="padding:24px;color:#a8a29e">Error: '+esc(e.message)+'</div>';
  });

  function renderProjectList(){
    var h='';
    for(var i=0;i<treeData.projects.length;i++){
      var p=treeData.projects[i];
      h+='<div class="project-card" onclick="openProject('+i+')">';
      h+='<div class="pc-name">'+esc(p.displayName)+'</div>';
      h+='<div class="pc-meta">';
      h+='<span>'+p.convCount+' conversation'+(p.convCount!==1?'s':'')+'</span>';
      h+='<span>'+p.msgCount+' messages</span>';
      h+='<span>'+fmtDate(p.lastActive)+'</span>';
      if(p.claudeMD) h+=' <span class="pc-badge">CLAUDE.md</span>';
      if(p.memoryFiles&&p.memoryFiles.length) h+=' <span class="pc-badge">Memory('+p.memoryFiles.length+')</span>';
      h+='</div>';
      if(p.conversations&&p.conversations.length>0){
        h+='<div class="pc-convs">';
        var show=Math.min(p.conversations.length,3);
        for(var ci=0;ci<show;ci++){
          var c=p.conversations[ci];
          h+='<div class="pc-conv">'+esc(c.title||c.slug||c.sessionId.substring(0,8))+'</div>';
        }
        if(p.conversations.length>3) h+='<div class="pc-conv" style="color:var(--text-muted)">+'+(p.conversations.length-3)+' more...</div>';
        h+='</div>';
      }
      h+='</div>';
    }
    if(treeData.plans&&treeData.plans.length>0){
      h+='<div class="project-card" style="border-style:dashed">';
      h+='<div class="pc-name" style="color:var(--text-muted)">Global Plans</div>';
      h+='<div class="pc-convs">';
      for(var pi=0;pi<treeData.plans.length;pi++){
        h+='<div class="pc-conv" style="cursor:pointer" onclick="event.stopPropagation();loadGlobalContent(\''+esc(treeData.plans[pi].path)+'\',\''+esc(treeData.plans[pi].name)+'\')">'+esc(treeData.plans[pi].name)+'</div>';
      }
      h+='</div></div>';
    }
    document.getElementById('project-list').innerHTML=h;
  }

  window.openProject=function(idx){
    currentProjIdx=idx;
    var proj=treeData.projects[idx];
    showScreen('screen-detail');
    document.getElementById('sb-projname').textContent=proj.displayName;

    // Badges
    var bh='';
    if(proj.claudeMD) bh+='<span class="sb-badge" onclick="loadFile(\''+esc(proj.claudeMD)+'\',\'CLAUDE.md\')">CLAUDE.md</span>';
    if(proj.memoryFiles){
      for(var mi=0;mi<proj.memoryFiles.length;mi++){
        var mf=proj.memoryFiles[mi];
        bh+='<span class="sb-badge" onclick="loadFile(\''+esc(mf.path)+'\',\''+esc(mf.name)+'\')">'+esc(mf.name.replace('.md',''))+'</span>';
      }
    }
    document.getElementById('sb-badges').innerHTML=bh;

    // Conversation list
    var lh='';
    for(var ci=0;ci<proj.conversations.length;ci++){
      var c=proj.conversations[ci];
      lh+='<div class="sb-conv" data-path="'+esc(c.path)+'" onclick="loadConv(this)">';
      lh+='<div class="sb-conv-title">'+esc(c.title||c.slug||c.sessionId.substring(0,8))+'</div>';
      lh+='<div class="sb-conv-meta">';
      lh+='<span>'+fmtDate(c.modTime)+'</span>';
      lh+='<span>'+c.msgCount+' msgs</span>';
      if(c.gitBranch) lh+='<span>'+esc(c.gitBranch)+'</span>';
      lh+='</div></div>';
      if(c.subAgents){
        for(var si=0;si<c.subAgents.length;si++){
          var sa=c.subAgents[si];
          var saDesc=sa.description||sa.name;
          var saType=sa.agentType||'agent';
          lh+='<div class="sb-agent" data-path="'+esc(sa.path)+'" onclick="loadConv(this)">';
          lh+='<span class="at">'+esc(saType)+'</span> <span style="overflow:hidden;text-overflow:ellipsis">'+esc(saDesc)+'</span>';
          lh+='</div>';
        }
      }
    }
    document.getElementById('sb-list').innerHTML=lh;

    // Reset content
    selectedPath=null;
    document.getElementById('ct-header').style.display='none';
    document.getElementById('export-btn').style.display='none';
    document.getElementById('ct-body').innerHTML='<div class="ct-empty">Select a conversation</div>';
  };

  window.loadConv=function(el){
    var path=el.getAttribute('data-path');
    var title=el.querySelector('.sb-conv-title');
    var titleText=title?title.textContent:(el.textContent||'').trim();
    selectedPath=path;

    // Update selection UI
    document.querySelectorAll('.sb-conv,.sb-agent').forEach(function(e){e.classList.remove('selected')});
    el.classList.add('selected');

    document.getElementById('ct-header').style.display='flex';
    document.getElementById('ct-title').textContent=titleText;
    document.getElementById('ct-sub').textContent='';
    document.getElementById('export-btn').style.display='inline-block';
    document.getElementById('ct-body').innerHTML='<div class="loading-wrap"><div class="spinner"></div> Loading...</div>';

    fetch('/api/messages?path='+encodeURIComponent(path)).then(function(r){return r.json()}).then(function(msgs){
      renderMessages(msgs||[]);
    }).catch(function(e){
      document.getElementById('ct-body').innerHTML='<div style="padding:28px;color:var(--text-muted)">Error: '+esc(e.message)+'</div>';
    });
  };

  window.loadFile=function(path,name){
    selectedPath=path;
    document.querySelectorAll('.sb-conv,.sb-agent').forEach(function(e){e.classList.remove('selected')});
    document.getElementById('ct-header').style.display='flex';
    document.getElementById('ct-title').textContent=name;
    document.getElementById('ct-sub').textContent='';
    document.getElementById('export-btn').style.display='none';
    document.getElementById('ct-body').innerHTML='<div class="loading-wrap"><div class="spinner"></div> Loading...</div>';

    fetch('/api/content?path='+encodeURIComponent(path)).then(function(r){return r.json()}).then(function(data){
      document.getElementById('ct-body').innerHTML='<div class="md-viewer">'+(data.html||'<p>'+esc(data.raw)+'</p>')+'</div>';
      document.getElementById('ct-body').scrollTop=0;
      document.querySelectorAll('#ct-body pre code').forEach(function(el){hljs.highlightElement(el)});
      // Intercept relative links in rendered markdown
      var dir=path.substring(0,path.lastIndexOf('/')+1);
      document.querySelectorAll('#ct-body .md-viewer a').forEach(function(a){
        var href=a.getAttribute('href');
        if(!href||href.indexOf('://')!==-1||href.charAt(0)==='#') return;
        a.addEventListener('click',function(e){
          e.preventDefault();
          var resolved=dir+href;
          loadFile(resolved,href);
        });
        a.style.cursor='pointer';
      });
    });
  };

  window.loadGlobalContent=function(path,name){
    // Show detail screen with minimal sidebar
    showScreen('screen-detail');
    document.getElementById('sb-projname').textContent='Global';
    document.getElementById('sb-badges').innerHTML='';
    document.getElementById('sb-list').innerHTML='';
    currentProjIdx=-1;
    loadFile(path,name);
  };

  function renderMessages(messages){
    var h='<div class="messages">';
    for(var i=0;i<messages.length;i++){
      var m=messages[i];
      if(m.type==='user'){
        h+='<div class="message"><div class="msg-header"><div class="avatar u">U</div><span class="msg-role">You</span><span class="msg-time">'+esc(m.timestamp)+'</span></div><div class="msg-body">'+m.html+'</div></div>';
      }else if(m.type==='assistant'){
        var ml=m.model?' <span class="msg-model">'+esc(m.model)+'</span>':'';
        h+='<div class="message"><div class="msg-header"><div class="avatar a">C</div><span class="msg-role">Claude</span>'+ml+'<span class="msg-time">'+esc(m.timestamp)+'</span></div><div class="msg-body">';
        if(m.thinking){
          var tid='th-'+i,wc=wordCount(m.thinking);
          h+='<div class="thinking-block"><div class="thinking-toggle" onclick="toggleThink(\''+tid+'\')"><span class="tg-arrow" id="tga-'+tid+'">&#9654;</span> Thinking... ('+wc+' words)</div><div class="thinking-content" id="'+tid+'">'+esc(m.thinking)+'</div></div>';
        }
        if(m.html) h+=m.html;
        if(m.tools&&m.tools.length){for(var t=0;t<m.tools.length;t++) h+='<div class="tool-call"><span class="tool-icon">&#9881;</span> '+esc(m.tools[t])+'</div>';}
        h+='</div>';
        if(m.tokens){var tok='in:'+m.tokens['in']+' out:'+m.tokens.out;if(m.tokens.cacheRead) tok+=' cache:'+m.tokens.cacheRead;h+='<div class="token-info">'+tok+'</div>';}
        h+='</div>';
      }else if(m.type==='system'){
        h+='<div class="system-msg">[system] '+m.html+'</div>';
      }
    }
    if(!messages.length) h+='<div style="padding:28px;color:var(--text-muted);text-align:center">No messages</div>';
    h+='</div>';
    var body=document.getElementById('ct-body');
    body.innerHTML=h;
    body.scrollTop=0;
    body.querySelectorAll('pre code').forEach(function(el){hljs.highlightElement(el)});
  }

  // Back button
  document.getElementById('back-btn').addEventListener('click',function(){
    showScreen('screen-projects');
    currentProjIdx=-1;selectedPath=null;
  });

  // Export
  document.getElementById('export-btn').addEventListener('click',function(){
    if(selectedPath) window.open('/api/export?path='+encodeURIComponent(selectedPath),'_blank');
  });
})();

function toggleThink(id){
  var el=document.getElementById(id),arrow=document.getElementById('tga-'+id);
  if(!el)return;el.classList.toggle('open');if(arrow)arrow.classList.toggle('open');
}
</script>
</body></html>` + ""
