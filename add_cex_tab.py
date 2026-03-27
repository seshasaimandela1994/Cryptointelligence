import re

with open('CryptoIntelligence_Live_Dashboard.html', 'r') as f:
    html = f.read()

# Add tab button
html = html.replace(
    "<button class=\"ntab\" onclick=\"goPage('cexv2',this)\">CEX REGISTRY</button>",
    "<button class=\"ntab\" onclick=\"goPage('cexv2',this)\">CEX REGISTRY</button>\n    <button class=\"ntab\" onclick=\"goPage('detection',this)\">DETECTION ENGINE</button>"
)

tab_html = '''
<!-- ══════════════ CEX DETECTION ENGINE TAB ══════════════ -->
<div class="page" id="page-detection">

  <!-- Header stats -->
  <div class="stats-grid sg-4" style="margin-bottom:14px">
    <div class="stat" style="--ac:var(--cyan)">
      <div class="stat-lbl">Scored Wallets</div>
      <div class="stat-val" id="det-scored">—</div>
      <div class="stat-sub">behavioral features</div>
    </div>
    <div class="stat" style="--ac:var(--green)">
      <div class="stat-lbl">High Confidence</div>
      <div class="stat-val" id="det-highconf" style="color:var(--green)">—</div>
      <div class="stat-sub">score ≥ 0.90</div>
    </div>
    <div class="stat" style="--ac:var(--amber)">
      <div class="stat-lbl">Exchange Labels</div>
      <div class="stat-val" id="det-labels">—</div>
      <div class="stat-sub">hot/cold/deposit</div>
    </div>
    <div class="stat" style="--ac:var(--purple)">
      <div class="stat-lbl">Transfers w/ USD</div>
      <div class="stat-val" id="det-usd">—</div>
      <div class="stat-sub">price-valued</div>
    </div>
  </div>

  <!-- Address screener -->
  <div class="panel" style="margin-bottom:14px">
    <div class="ph"><span class="pt">🔍 CEX DETECTION SCREEN</span></div>
    <div class="pb">
      <div class="row" style="margin-bottom:8px">
        <input class="inp" id="detAddr" placeholder="Enter address to screen against CEX detection engine..."
          onkeydown="if(event.key==='Enter')detScreen()"/>
        <button class="btn" onclick="detScreen()">SCREEN</button>
      </div>
      <div class="qrow" style="margin-bottom:10px">
        <button class="qb" onclick="detSet('0xa9d1e08c7793af67e9d92fe308d5697fb81d3e43')">$1.3B Hot</button>
        <button class="qb" onclick="detSet('0x28c6c06298d514db089934071355e5743bf21d60')">Binance Hot</button>
        <button class="qb" onclick="detSet('0x88e6a0c2ddd26feeb64f039a2c41296fcb3f5640')">Uniswap V3</button>
        <button class="qb" onclick="detSet('0xbbbbbbbbbb9cc5e90e3b3af64bdaf62c37eeffcb')">Morpho $16B</button>
      </div>
      <div id="detResult" style="display:none"></div>
    </div>
  </div>

  <!-- Top candidates + Exchange lookup -->
  <div class="g2" style="margin-bottom:14px">

    <!-- Top hot wallets -->
    <div class="panel">
      <div class="ph">
        <span class="pt"><div class="pulse-dot"></div>TOP HOT WALLET CANDIDATES</span>
        <div style="display:flex;gap:6px">
          <button class="qb active" onclick="detLoadRole('hot',this)">HOT</button>
          <button class="qb" onclick="detLoadRole('deposit',this)">DEPOSIT</button>
          <button class="qb" onclick="detLoadRole('cold',this)">COLD</button>
        </div>
      </div>
      <div style="overflow-y:auto;max-height:320px">
        <table class="tbl" id="detCandTable">
          <thead><tr>
            <th>ADDRESS</th><th>SCORE</th><th>USD 30D</th><th>SENDERS</th><th>KNOWN</th>
          </tr></thead>
          <tbody id="detCandBody"></tbody>
        </table>
      </div>
    </div>

    <!-- Exchange lookup -->
    <div class="panel">
      <div class="ph"><span class="pt">EXCHANGE INTELLIGENCE</span></div>
      <div class="pb">
        <div class="row" style="margin-bottom:10px">
          <input class="inp" id="detExchName" placeholder="Exchange name (Binance, Coinbase, Kraken...)"
            onkeydown="if(event.key==='Enter')detLoadExchange()"/>
          <button class="btn" onclick="detLoadExchange()">LOOKUP</button>
        </div>
        <div class="qrow" style="margin-bottom:10px">
          <button class="qb" onclick="detExchSet('Binance')">Binance</button>
          <button class="qb" onclick="detExchSet('Coinbase')">Coinbase</button>
          <button class="qb" onclick="detExchSet('Kraken')">Kraken</button>
          <button class="qb" onclick="detExchSet('OKX')">OKX</button>
        </div>
        <div id="detExchResult" style="overflow-y:auto;max-height:220px"></div>
      </div>
    </div>
  </div>

  <!-- Candidates full table -->
  <div class="panel">
    <div class="ph">
      <span class="pt">ALL HIGH-CONFIDENCE CANDIDATES (score ≥ 0.90)</span>
      <button class="btn btn-secondary" style="font-size:9px;padding:3px 8px" onclick="detLoadCandidates()">↻ REFRESH</button>
    </div>
    <div style="overflow-x:auto;max-height:300px;overflow-y:auto">
      <table class="tbl" id="detAllTable">
        <thead><tr>
          <th>#</th><th>ADDRESS</th><th>BEST ROLE</th><th>SCORE</th>
          <th>DEPOSIT</th><th>HOT</th><th>OUTBOUND USD</th>
          <th>SENDERS</th><th>RECEIVERS</th><th>EXCHANGE</th>
        </tr></thead>
        <tbody id="detAllBody"></tbody>
      </table>
    </div>
  </div>
</div>
'''

html = html.replace('</div><!-- /wrap -->', tab_html + '\n</div><!-- /wrap -->', 1)

js = '''
const CEX_API = 'http://localhost:8084';

function detSet(a){document.getElementById('detAddr').value=a;detScreen();}
function detExchSet(n){document.getElementById('detExchName').value=n;detLoadExchange();}

function fmtUSD(n){
  if(!n||n===0)return '—';
  if(n>=1e9)return '$'+(n/1e9).toFixed(1)+'B';
  if(n>=1e6)return '$'+(n/1e6).toFixed(1)+'M';
  if(n>=1e3)return '$'+(n/1e3).toFixed(0)+'K';
  return '$'+n.toFixed(0);
}

async function detLoadStats(){
  try{
    const d=await fetch(CEX_API+'/v1/cex/stats').then(r=>r.json());
    document.getElementById('det-scored').textContent=fmt(d.scored_wallets);
    document.getElementById('det-highconf').textContent=fmt(d.high_confidence_candidates);
    document.getElementById('det-labels').textContent=fmt(d.labeled_exchange_wallets);
    document.getElementById('det-usd').textContent=fmt(d.transfers_with_usd);
  }catch(e){console.log('det stats:',e);}
}

async function detScreen(){
  const addr=document.getElementById('detAddr').value.trim();
  if(!addr)return;
  const el=document.getElementById('detResult');
  el.style.display='block';
  el.innerHTML='<div style="font-family:var(--mono);font-size:10px;color:var(--cyan)">Screening...</div>';
  try{
    const d=await fetch(CEX_API+'/v1/cex/screen/'+addr).then(r=>r.json());
    const c=d.candidate_scores;
    const isKnown=d.is_exchange_wallet;
    const borderCol=isKnown?'var(--green)':c&&c.best_candidate_score>=0.90?'var(--amber)':'var(--bdr)';

    el.innerHTML=`
      <div style="border:1px solid ${borderCol};padding:12px;background:var(--bg);margin-bottom:8px">
        <div style="display:grid;grid-template-columns:repeat(4,1fr);gap:8px;margin-bottom:10px">
          <div>
            <div style="font-family:var(--mono);font-size:7px;color:var(--muted2);margin-bottom:2px">STATUS</div>
            <div style="font-family:var(--mono);font-size:11px;font-weight:700;color:${isKnown?'var(--green)':'var(--amber)'}">
              ${isKnown?'✓ KNOWN EXCHANGE':'⚡ CANDIDATE'}
            </div>
          </div>
          ${isKnown?`
          <div>
            <div style="font-family:var(--mono);font-size:7px;color:var(--muted2);margin-bottom:2px">EXCHANGE</div>
            <div style="font-family:var(--mono);font-size:11px;font-weight:700;color:var(--cyan)">${d.exchange}</div>
          </div>
          <div>
            <div style="font-family:var(--mono);font-size:7px;color:var(--muted2);margin-bottom:2px">ROLE</div>
            <div style="font-family:var(--mono);font-size:9px;color:var(--amber)">${d.wallet_role||'—'}</div>
          </div>
          <div>
            <div style="font-family:var(--mono);font-size:7px;color:var(--muted2);margin-bottom:2px">CONFIDENCE</div>
            <div style="font-family:var(--mono);font-size:14px;font-weight:700;color:var(--green)">${((d.confidence||0)*100).toFixed(0)}%</div>
          </div>`:''}
          ${c?`
          <div>
            <div style="font-family:var(--mono);font-size:7px;color:var(--muted2);margin-bottom:2px">BEST ROLE</div>
            <div style="font-family:var(--mono);font-size:9px;color:var(--amber)">${c.best_candidate_role}</div>
          </div>
          <div>
            <div style="font-family:var(--mono);font-size:7px;color:var(--muted2);margin-bottom:2px">SCORE</div>
            <div style="font-family:var(--mono);font-size:14px;font-weight:700;color:var(--cyan)">${c.best_candidate_score}</div>
          </div>
          <div>
            <div style="font-family:var(--mono);font-size:7px;color:var(--muted2);margin-bottom:2px">OUTBOUND USD</div>
            <div style="font-family:var(--mono);font-size:11px;font-weight:700;color:var(--green)">${fmtUSD(c.outbound_usd_30d)}</div>
          </div>
          <div>
            <div style="font-family:var(--mono);font-size:7px;color:var(--muted2);margin-bottom:2px">SENDERS</div>
            <div style="font-family:var(--mono);font-size:11px;font-weight:700">${fmt(c.unique_senders_30d)}</div>
          </div>`:''}
        </div>
        ${c?`<div style="display:flex;gap:8px;margin-bottom:8px">
          <div style="flex:1;background:var(--surface);padding:6px;text-align:center">
            <div style="font-family:var(--mono);font-size:7px;color:var(--muted2)">DEPOSIT SCORE</div>
            <div style="font-family:var(--mono);font-size:13px;font-weight:700;color:${c.deposit_score>=0.75?'var(--green)':c.deposit_score>=0.5?'var(--amber)':'var(--muted2)'}">${c.deposit_score}</div>
          </div>
          <div style="flex:1;background:var(--surface);padding:6px;text-align:center">
            <div style="font-family:var(--mono);font-size:7px;color:var(--muted2)">HOT SCORE</div>
            <div style="font-family:var(--mono);font-size:13px;font-weight:700;color:${c.hot_score>=0.75?'var(--cyan)':c.hot_score>=0.5?'var(--amber)':'var(--muted2)'}">${c.hot_score}</div>
          </div>
          <div style="flex:1;background:var(--surface);padding:6px;text-align:center">
            <div style="font-family:var(--mono);font-size:7px;color:var(--muted2)">COLD SCORE</div>
            <div style="font-family:var(--mono);font-size:13px;font-weight:700;color:${c.cold_score>=0.75?'var(--blue)':c.cold_score>=0.5?'var(--amber)':'var(--muted2)'}">${c.cold_score}</div>
          </div>
          <div style="flex:1;background:var(--surface);padding:6px;text-align:center">
            <div style="font-family:var(--mono);font-size:7px;color:var(--muted2)">RECEIVERS</div>
            <div style="font-family:var(--mono);font-size:13px;font-weight:700">${fmt(c.unique_receivers_30d)}</div>
          </div>
        </div>`:''}
        ${d.risk_signals&&d.risk_signals.length?`
        <div style="display:flex;flex-wrap:wrap;gap:4px">
          ${d.risk_signals.map(s=>`<span style="font-family:var(--mono);font-size:8px;padding:2px 8px;border:1px solid var(--amber);color:var(--amber)">${s}</span>`).join('')}
        </div>`:''}
      </div>`;
  }catch(e){el.innerHTML=`<div style="color:var(--red);font-family:var(--mono);font-size:10px">Error: ${e.message}</div>`;}
}

async function detLoadRole(role, btn){
  document.querySelectorAll('#page-detection .qb').forEach(b=>b.classList.remove('active'));
  if(btn)btn.classList.add('active');
  try{
    const d=await fetch(CEX_API+'/v1/cex/top/'+role+'?limit=20').then(r=>r.json());
    const rc={deposit:'var(--green)',hot:'var(--cyan)',cold:'var(--blue)'}[role]||'var(--cyan)';
    document.getElementById('detCandBody').innerHTML=(d.results||[]).map((r,i)=>`
      <tr onclick="detSet('${r.address}')" style="cursor:pointer">
        <td style="font-family:var(--mono);font-size:9px;color:var(--muted2)">${r.address.slice(0,10)}...${r.address.slice(-6)}</td>
        <td style="color:${rc};font-weight:700">${r.score}</td>
        <td style="color:var(--green)">${fmtUSD(r.usd_value_30d)}</td>
        <td>${fmt(r.unique_senders_30d)}</td>
        <td style="color:${r.known_exchange!=='NOT LABELED'?'var(--cyan)':'var(--muted2)';};font-size:9px">${r.known_exchange}</td>
      </tr>`).join('');
  }catch(e){console.log('detLoadRole:',e);}
}

async function detLoadExchange(){
  const name=document.getElementById('detExchName').value.trim();
  if(!name)return;
  const el=document.getElementById('detExchResult');
  el.innerHTML='<div style="font-family:var(--mono);font-size:9px;color:var(--cyan)">Loading...</div>';
  try{
    const d=await fetch(CEX_API+'/v1/cex/exchange/'+encodeURIComponent(name)).then(r=>r.json());
    if(d.error){el.innerHTML=`<div style="color:var(--red);font-family:var(--mono);font-size:9px">${d.error}</div>`;return;}
    const rc={regulated:'var(--green)',mixed:'var(--amber)',offshore:'var(--red)',unknown:'var(--muted2)'}[d.regulatory_status]||'var(--muted2)';
    el.innerHTML=`
      <div style="margin-bottom:8px;padding:8px;background:var(--bg);border:1px solid var(--bdr)">
        <div style="display:flex;justify-content:space-between;align-items:center;margin-bottom:6px">
          <span style="font-family:var(--mono);font-size:13px;font-weight:700;color:var(--cyan)">${d.canonical_name}</span>
          <span style="font-family:var(--mono);font-size:8px;padding:2px 8px;border:1px solid ${rc};color:${rc}">${d.regulatory_status}</span>
        </div>
        <div style="display:grid;grid-template-columns:repeat(4,1fr);gap:4px;margin-bottom:6px">
          <div style="text-align:center">
            <div style="font-family:var(--mono);font-size:7px;color:var(--muted2)">TIER</div>
            <div style="font-family:var(--mono);font-size:10px;color:var(--cyan)">${d.trust_tier}</div>
          </div>
          <div style="text-align:center">
            <div style="font-family:var(--mono);font-size:7px;color:var(--muted2)">WALLETS</div>
            <div style="font-family:var(--mono);font-size:10px;font-weight:700">${d.labeled_wallets}</div>
          </div>
          <div style="text-align:center">
            <div style="font-family:var(--mono);font-size:7px;color:var(--muted2)">AVG CONF</div>
            <div style="font-family:var(--mono);font-size:10px;color:var(--green)">${(d.avg_confidence*100).toFixed(0)}%</div>
          </div>
          <div style="text-align:center">
            <div style="font-family:var(--mono);font-size:7px;color:var(--muted2)">POR</div>
            <div style="font-family:var(--mono);font-size:10px;color:${d.proof_of_reserves?'var(--green)':'var(--muted2)'}">${d.proof_of_reserves?'✓':'—'}</div>
          </div>
        </div>
      </div>
      ${(d.wallets||[]).map(w=>`
        <div style="display:flex;align-items:center;gap:6px;padding:5px 0;border-bottom:1px solid rgba(255,255,255,.03);cursor:pointer"
          onclick="detSet('${w.address}')">
          <span style="font-family:var(--mono);font-size:8px;color:var(--muted2);flex:1">${w.address.slice(0,12)}...${w.address.slice(-6)}</span>
          <span style="font-family:var(--mono);font-size:7px;padding:1px 5px;border:1px solid ${w.wallet_role.includes('cold')?'var(--blue)':'var(--cyan)'};color:${w.wallet_role.includes('cold')?'var(--blue)':'var(--cyan)'}">${w.wallet_role.replace('exchange_','')}</span>
          <span style="font-family:var(--mono);font-size:8px;color:var(--green)">${(w.confidence_score*100).toFixed(0)}%</span>
          ${w.outbound_usd_30d>0?`<span style="font-family:var(--mono);font-size:8px;color:var(--amber)">${fmtUSD(w.outbound_usd_30d)}</span>`:''}
        </div>`).join('')}`;
  }catch(e){el.innerHTML=`<div style="color:var(--red);font-family:var(--mono);font-size:9px">Error: ${e.message}</div>`;}
}

async function detLoadCandidates(){
  try{
    const d=await fetch(CEX_API+'/v1/cex/candidates?limit=100&min_score=0.90').then(r=>r.json());
    document.getElementById('detAllBody').innerHTML=(d.candidates||[]).map((c,i)=>`
      <tr onclick="detSet('${c.address}')" style="cursor:pointer">
        <td style="color:var(--muted2)">${i+1}</td>
        <td style="font-family:var(--mono);font-size:9px">${c.address.slice(0,10)}...${c.address.slice(-6)}</td>
        <td style="font-size:8px;color:var(--amber)">${c.best_candidate_role.replace('exchange_','')}</td>
        <td style="font-weight:700;color:var(--cyan)">${c.best_candidate_score}</td>
        <td style="color:var(--green)">${c.deposit_score}</td>
        <td style="color:var(--cyan)">${c.hot_score}</td>
        <td style="color:var(--amber)">${fmtUSD(c.outbound_usd_30d)}</td>
        <td>${fmt(c.unique_senders_30d)}</td>
        <td>${fmt(c.unique_receivers_30d)}</td>
        <td style="font-size:8px;color:${c.known_exchange?'var(--cyan)':'var(--muted2)'}">${c.known_exchange||'—'}</td>
      </tr>`).join('');
  }catch(e){console.log('detLoadCandidates:',e);}
}

// Load on tab switch
const _origGoPage = typeof goPage === 'function' ? goPage : null;
'''

html = html.replace(
    "if(name==='cexv2')renderCexV2Table(cexV2Data);",
    "if(name==='cexv2')renderCexV2Table(cexV2Data);\n  if(name==='detection'){detLoadStats();detLoadRole('hot',null);detLoadCandidates();}"
)

html = html.replace(
    '// INIT\ncheckAPI();',
    js + '\ndetLoadStats();\n// INIT\ncheckAPI();'
)

with open('CryptoIntelligence_Live_Dashboard.html', 'w') as f:
    f.write(html)
print(f"Done: {len(html)} chars")
