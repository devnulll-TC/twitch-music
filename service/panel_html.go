package main

// Шаблоны подставляются простой заменой, а НЕ через Fprintf: в CSS полно процентов
// (width:100%, translateX(-50%)), и форматирование принимало их за подстановки —
// go vet ловит это как "unknown verb".
//
// Оформление — токены лендинга devnulll.dev (src/styles/global.css): та же палитра,
// точечная сетка с плёночным зерном на фоне, кнопки со свечением, моноширинный шрифт
// для технических строк. Панель должна выглядеть частью продукта, а не отдельной поделкой.
const brandCSS = `
 :root{
  --primary:#8B5CF6; --primary-hover:#7C3AED; --on-primary:#fff;
  --bg:#0C0C10; --surface:#16161B; --surface-2:#1E1E26;
  --text:#ECECF1; --text-muted:#9A9AA5; --border:#2A2A33;
  --radius:10px; --radius-lg:16px;
  --font:'Inter',system-ui,-apple-system,sans-serif;
  --mono:'JetBrains Mono',ui-monospace,'SF Mono',Menlo,monospace;
  --ok:#34D399; --bad:#F87171;
  color-scheme:dark;
 }
 *{box-sizing:border-box;margin:0;padding:0}
 body{font-family:var(--font);background:var(--bg);color:var(--text);line-height:1.5;
      -webkit-font-smoothing:antialiased;overflow-x:hidden}

 /* Фон как на лендинге: точечная сетка + плёночное зерно, оба слоя статичные. */
 body::before{content:'';position:fixed;inset:0;z-index:-2;pointer-events:none;
  background-image:radial-gradient(color-mix(in srgb,var(--border) 55%,transparent) 1px,transparent 1px);
  background-size:30px 30px;
  -webkit-mask-image:radial-gradient(ellipse 120% 90% at 50% 0%,black 25%,rgba(0,0,0,.4) 65%,rgba(0,0,0,.12) 100%);
  mask-image:radial-gradient(ellipse 120% 90% at 50% 0%,black 25%,rgba(0,0,0,.4) 65%,rgba(0,0,0,.12) 100%)}
 body::after{content:'';position:fixed;inset:0;z-index:-1;pointer-events:none;opacity:.04;
  background-size:160px 160px;
  background-image:url("data:image/svg+xml;utf8,<svg xmlns='http://www.w3.org/2000/svg' width='160' height='160'><filter id='n'><feTurbulence type='fractalNoise' baseFrequency='0.85' numOctaves='2' stitchTiles='stitch'/></filter><rect width='100%25' height='100%25' filter='url(%23n)'/></svg>")}

 .btn{display:inline-flex;align-items:center;gap:8px;padding:10px 18px;border-radius:var(--radius);
   font:inherit;font-weight:600;font-size:14px;cursor:pointer;border:1px solid transparent;
   white-space:nowrap;color:var(--text);background:transparent;
   transition:background .15s,border-color .15s,transform .1s,box-shadow .2s}
 .btn:active{transform:translateY(1px)}
 .btn-primary{background:var(--primary);color:var(--on-primary)}
 .btn-primary:hover{background:var(--primary-hover);
   box-shadow:0 8px 30px color-mix(in srgb,var(--primary) 35%,transparent)}
 .btn-ghost{border-color:var(--border)}
 .btn-ghost:hover{border-color:var(--primary);color:var(--primary)}
 .btn-danger{border-color:var(--border);color:var(--text-muted)}
 .btn-danger:hover{border-color:var(--bad);color:var(--bad)}
 .btn-sm{padding:5px 10px;font-size:13px}
 /* Иконочная кнопка: квадратная, без текстовых отступов по бокам. */
 .btn-ic{padding:7px;line-height:0}
 .ic{width:16px;height:16px;display:block;flex:none;
     fill:none;stroke:currentColor;stroke-width:2;stroke-linecap:round;stroke-linejoin:round}
 .ic-sm{width:12px;height:12px} .ic-14{width:14px;height:14px}
`

// Иконки Lucide (ISC), вшиты спрайтом вместо эмодзи: системные эмодзи во встроенном
// браузере OBS рисуются чужим шрифтом и выбиваются из брендинга, а тянуть их с CDN
// в док нельзя — источник должен работать без внешних запросов.
const iconsSVG = `<svg xmlns="http://www.w3.org/2000/svg" style="display:none" aria-hidden="true">
<symbol id="i-play" viewBox="0 0 24 24"><polygon points="5 3 19 12 5 21 5 3"/></symbol>
<symbol id="i-pause" viewBox="0 0 24 24"><rect x="6" y="4" width="4" height="16" rx="1"/><rect x="14" y="4" width="4" height="16" rx="1"/></symbol>
<symbol id="i-skip" viewBox="0 0 24 24"><polygon points="5 4 15 12 5 20 5 4"/><line x1="19" y1="5" x2="19" y2="19"/></symbol>
<symbol id="i-vol" viewBox="0 0 24 24"><polygon points="11 5 6 9 2 9 2 15 6 15 11 19 11 5"/><path d="M15.54 8.46a5 5 0 0 1 0 7.07"/><path d="M19.07 4.93a10 10 0 0 1 0 14.14"/></symbol>
<symbol id="i-mute" viewBox="0 0 24 24"><polygon points="11 5 6 9 2 9 2 15 6 15 11 19 11 5"/><line x1="22" y1="9" x2="16" y2="15"/><line x1="16" y1="9" x2="22" y2="15"/></symbol>
<symbol id="i-head" viewBox="0 0 24 24"><path d="M3 14h3a2 2 0 0 1 2 2v3a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-7a9 9 0 0 1 18 0v7a2 2 0 0 1-2 2h-1a2 2 0 0 1-2-2v-3a2 2 0 0 1 2-2h3"/></symbol>
<symbol id="i-gift" viewBox="0 0 24 24"><rect x="3" y="8" width="18" height="4" rx="1"/><path d="M12 8v13"/><path d="M19 12v7a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2v-7"/><path d="M7.5 8a2.5 2.5 0 0 1 0-5A4.8 8 0 0 1 12 8a4.8 8 0 0 1 4.5-5 2.5 2.5 0 0 1 0 5"/></symbol>
<symbol id="i-up" viewBox="0 0 24 24"><path d="m18 15-6-6-6 6"/></symbol>
<symbol id="i-down" viewBox="0 0 24 24"><path d="m6 9 6 6 6-6"/></symbol>
<symbol id="i-x" viewBox="0 0 24 24"><path d="M18 6 6 18"/><path d="m6 6 12 12"/></symbol>
</svg>`

const loginPageHTML = `<!doctype html><meta charset="utf-8"><title>dev&gt;nulll music</title>
<meta name="viewport" content="width=device-width,initial-scale=1">
<link rel="preconnect" href="https://fonts.googleapis.com"><link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
<link rel="stylesheet" href="https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600;700&family=JetBrains+Mono:wght@400;500;700&display=swap">
<style>{{BRAND}}
 body{display:grid;place-items:center;min-height:100vh;text-align:center;padding:24px}
 .logo{font-family:var(--mono);font-size:26px;font-weight:700;letter-spacing:-.5px}
 .logo b{color:var(--primary)}
 .cursor{display:inline-block;width:10px;background:var(--primary);animation:blink 1.1s step-end infinite}
 @keyframes blink{50%{opacity:0}}
 p{color:var(--text-muted);margin:10px 0 26px}
 small{color:var(--text-muted);display:block;margin-top:26px;max-width:400px;font-size:13px}
</style>
<div>
 <div class="logo">dev<b>&gt;</b>nulll music<span class="cursor">&nbsp;</span></div>
 <p>Панель управления музыкой в эфире</p>
 <a class="btn btn-primary" href="{{LOGIN}}">Войти через Twitch</a>
 <small>Доступ есть у стримера и назначенных модераторов. Прав у Twitch не просим вовсе —
 только подтверждение, кто ты. Писать от твоего имени бот не сможет.</small>
</div>`

const panelHTML = `<!doctype html><meta charset="utf-8"><title>dev&gt;nulll music — панель</title>
<meta name="viewport" content="width=device-width,initial-scale=1">
<link rel="preconnect" href="https://fonts.googleapis.com"><link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
<link rel="stylesheet" href="https://fonts.googleapis.com/css2?family=Inter:wght@400;500;600;700&family=JetBrains+Mono:wght@400;500;700&display=swap">
<style>{{BRAND}}
 body{padding:20px 16px 40px;max-width:920px;margin-inline:auto}
 header{display:flex;justify-content:space-between;align-items:center;gap:12px;
        margin-bottom:22px;flex-wrap:wrap}
 .logo{font-family:var(--mono);font-size:19px;font-weight:700}
 .logo b{color:var(--primary)}
 .sub{color:var(--text-muted);font-size:13px;font-family:var(--mono)}
 .card{background:color-mix(in srgb,var(--surface) 86%,transparent);
   border:1px solid var(--border);border-radius:var(--radius-lg);padding:18px;margin-bottom:14px;
   animation:rise .35s ease both}
 @keyframes rise{from{opacity:0;transform:translateY(8px)}to{opacity:1;transform:none}}
 .card:nth-of-type(2){animation-delay:.04s}.card:nth-of-type(3){animation-delay:.08s}
 .card:nth-of-type(4){animation-delay:.12s}
 .lbl{color:var(--text-muted);font-size:12px;text-transform:uppercase;letter-spacing:.6px;
      margin-bottom:8px;font-weight:600}
 .row{display:flex;gap:10px;align-items:center;flex-wrap:wrap}
 .between{justify-content:space-between}
 input{background:var(--bg);border:1px solid var(--border);color:var(--text);
   border-radius:var(--radius);padding:10px 13px;font:inherit;font-size:14px;
   transition:border-color .15s}
 input:focus{outline:none;border-color:var(--primary)}
 #q{flex:1;min-width:200px}
 code{font-family:var(--mono);background:var(--bg);border:1px solid var(--border);
   padding:11px 13px;border-radius:var(--radius);display:block;overflow-x:auto;
   white-space:nowrap;font-size:12.5px;color:var(--text-muted)}
 .np{font-size:19px;font-weight:600;margin-top:2px}
 .npby{color:var(--text-muted);font-size:13px;margin-top:4px}
 /* Полоса до конца трека — единственная «живая» деталь: панель висит у модера на
    втором мониторе, и мельтешить она не должна. */
 .bar{height:3px;background:var(--surface-2);border-radius:2px;margin-top:14px;overflow:hidden}
 .bar i{display:block;height:100%;background:var(--primary);
        box-shadow:0 0 12px color-mix(in srgb,var(--primary) 60%,transparent);
        transition:width 1s linear}
 table{width:100%;border-collapse:collapse}
 td{padding:11px 6px;border-top:1px solid var(--border);vertical-align:middle}
 tr:first-child td{border-top:0}
 .t{font-weight:600;font-size:14.5px}
 .meta{color:var(--text-muted);font-size:12.5px;margin-top:2px;font-family:var(--mono)}
 .num{color:var(--text-muted);font-family:var(--mono);font-size:13px;width:26px}
 .dot{width:7px;height:7px;border-radius:999px;display:inline-block;margin-right:7px}
 .dot.on{background:var(--ok);box-shadow:0 0 0 0 color-mix(in srgb,var(--ok) 60%,transparent);
         animation:pulse 2s infinite}
 .dot.off{background:var(--bad)}
 @keyframes pulse{70%{box-shadow:0 0 0 7px transparent}100%{box-shadow:0 0 0 0 transparent}}
 .state{font-size:13px;color:var(--text-muted)}
 .pill{font-family:var(--mono);font-size:12px;padding:4px 10px;border-radius:999px;
       border:1px solid var(--border);color:var(--text-muted)}
 .hint{color:var(--text-muted);font-size:12px;margin-top:10px}
 #msg{position:fixed;left:50%;transform:translateX(-50%) translateY(80px);bottom:24px;
   background:var(--surface-2);border:1px solid var(--border);padding:11px 20px;
   border-radius:var(--radius);opacity:0;transition:opacity .2s,transform .2s;
   pointer-events:none;z-index:9;font-size:14px}
 #msg.show{opacity:1;transform:translateX(-50%) translateY(0)}
 #msg.bad{border-color:var(--bad);color:var(--bad)}
 @media(max-width:600px){.num{display:none}}
 @media(prefers-reduced-motion:reduce){*{animation:none!important;transition:none!important}}
</style>
{{ICONS}}

<header>
 <div><div class="logo">dev<b>&gt;</b>nulll music</div><div class="sub" id="chan"></div></div>
 <div class="row"><span class="pill">{{WHO}}</span>
  <a class="btn btn-ghost btn-sm" href="{{BASE}}/panel/logout">выйти</a></div>
</header>

<div class="card">
 <div class="lbl">Звук для OBS</div>
 <code id="url">…</code>
 <div class="row" style="margin-top:12px">
  <button class="btn btn-primary" onclick="copyUrl()">Скопировать адрес</button>
  <span class="state" id="alive"></span>
 </div>
 <div class="hint">OBS → Sources → Media Source → снять «Local File» → вставить в Input.
  Громкость дальше крутится штатным ползунком OBS.</div>

 <div class="lbl" style="margin-top:18px">Звук только для наушников стримера</div>
 <code id="murl">…</code>
 <div class="row" style="margin-top:10px">
  <button class="btn btn-ghost" onclick="copyMon()">Скопировать адрес</button>
 </div>
 <div class="hint">Добавить вторым Media Source и поставить ему
  Audio Monitoring = <b>Monitor Only (mute output)</b>, а первому — <b>Monitor Off</b>.
  Тогда громкость в наушниках регулируется отдельно от эфира, ползунками в доке.</div>

 <div class="lbl" style="margin-top:18px">Панель внутри OBS</div>
 <code id="dock">…</code>
 <div class="row" style="margin-top:10px">
  <button class="btn btn-ghost" onclick="copyDock()">Скопировать ссылку дока</button>
 </div>
 <div class="hint">OBS → View → Docks → Custom Browser Docks → вставить ссылку.
  Появится панель прямо в OBS: что играет, очередь, скип и громкость. Вход не нужен —
  ключ уже в ссылке, поэтому её никому не пересылать.</div>
</div>

<div class="card">
 <div class="lbl">Громкость эфира</div>
 <div class="row">
  <input type="range" id="vol" min="0" max="150" value="100" style="flex:1;accent-color:var(--primary)"
         oninput="volLabel(this.value)" onchange="setVol(this.value)">
  <span class="state" id="vollbl" style="width:46px;text-align:right">100%</span>
 </div>
 <div class="hint">Это громкость самого потока — работает и для модератора, у которого
  нет доступа к OBS стримера. Ползунок в OBS при этом продолжает действовать поверх.</div>
</div>

<div class="card">
 <div class="lbl">Сейчас играет</div>
 <div class="np" id="np">—</div>
 <div class="npby" id="npby"></div>
 <div class="bar"><i id="bar" style="width:0"></i></div>
 <div class="row" style="margin-top:16px">
  <button class="btn btn-primary" id="pp" onclick="togglePause()">Пауза</button>
  <button class="btn btn-ghost" onclick="act('skip')">Пропустить</button>
  <button class="btn btn-danger" onclick="if(confirm('Очистить всю очередь?'))act('clear')">Очистить очередь</button>
 </div>
</div>

<div class="card">
 <div class="row between">
  <div><div class="lbl" style="margin:0">Награда за баллы</div>
   <div class="state" id="rwstate">…</div></div>
  <button id="rw" class="btn btn-primary" onclick="toggleReward()">…</button>
 </div>
 <div class="row" style="margin-top:16px">
  <label class="state">Цена, баллов
   <input id="cost" type="number" min="1" step="10" style="width:110px;margin-left:8px"></label>
  <label class="state">Лимит длины, минут
   <input id="maxdur" type="number" min="1" step="1" style="width:100px;margin-left:8px"></label>
  <button class="btn btn-ghost" onclick="saveSettings()">Сохранить</button>
 </div>
 <div class="hint">Трек длиннее лимита бот не возьмёт и вернёт баллы автоматически.</div>
</div>

<div class="card">
 <div class="row" style="margin-bottom:14px">
  <input id="q" placeholder="Ссылка или название трека · sc: — искать в SoundCloud"
         onkeydown="if(event.key==='Enter')add()">
  <button class="btn btn-primary" onclick="add()">Добавить</button>
 </div>
 <table id="tbl"></table>
 <div class="state" id="empty">Очередь пуста.</div>
</div>

<div id="msg"></div>
<script>
const BASE = "{{BASE}}";
let rewardOn = false;
let dirty = false;   // пока правишь поля, автообновление их не перетирает
let trackTotal = 0;
let volDragging = false;   // пока тянешь ползунок, автообновление его не дёргает
let paused = false;

function toast(t, bad){const m=document.getElementById('msg');m.textContent=t;
 m.className = 'show' + (bad ? ' bad' : '');
 clearTimeout(window._t); window._t=setTimeout(()=>m.className='', 3500);}

async function act(name, params={}){
 const r = await fetch(BASE+'/api/panel/'+name, {method:'POST', body:new URLSearchParams(params)});
 const d = await r.json().catch(()=>({}));
 if(!r.ok){ toast(d.error || 'ошибка', true); } else if(name==='add'){ toast('Добавлено: '+(d.title||'')); }
 load();
}
function add(){const i=document.getElementById('q');if(!i.value.trim())return;
 toast('Ищу…'); act('add',{q:i.value.trim()}); i.value='';}
function toggleReward(){act('settings',{reward_enabled:(!rewardOn).toString()});}
function saveSettings(){
 const cost=document.getElementById('cost').value.trim();
 const mins=document.getElementById('maxdur').value.trim();
 if(!cost||!mins){toast('Заполни оба поля',true);return;}
 act('settings',{reward_cost:cost, max_duration:String(Math.round(parseFloat(mins)*60))});
}
function copyUrl(){navigator.clipboard.writeText(document.getElementById('url').textContent)
 .then(()=>toast('Адрес скопирован'));}
function copyDock(){navigator.clipboard.writeText(document.getElementById('dock').textContent)
 .then(()=>toast('Ссылка дока скопирована'));}
function copyMon(){navigator.clipboard.writeText(document.getElementById('murl').textContent)
 .then(()=>toast('Адрес для наушников скопирован'));}
function volLabel(v){document.getElementById('vollbl').textContent=v+'%';}
function setVol(v){volDragging=false;act('volume',{v:String(v/100)});}
function togglePause(){act(paused?'resume':'pause');}
function fmt(s){s=Math.max(0,Math.round(s));return Math.floor(s/60)+':'+String(s%60).padStart(2,'0');}
function icon(name, cls){return '<svg class="ic'+(cls?' '+cls:'')+'"><use href="#i-'+name+'"/></svg>';}

async function load(){
 let d; try{ d = await (await fetch(BASE+'/api/panel/state')).json(); }catch(e){ return; }
 if(d.error){ location.reload(); return; }
 document.getElementById('chan').textContent = 'канал ' + d.channel;
 document.getElementById('url').textContent = d.stream_url;
 document.getElementById('dock').textContent = d.dock_url;
 document.getElementById('murl').textContent = d.monitor_url;
 if(!volDragging && typeof d.volume === 'number'){
   const v = Math.round(d.volume*100);
   document.getElementById('vol').value = v; volLabel(v);
 }
 const lis = (typeof d.listeners === 'number') ? d.listeners : -1;
 document.getElementById('alive').innerHTML = !d.alive
   ? '<span class="dot off"></span>поток не отвечает'
   : (lis > 0 ? '<span class="dot on"></span>OBS подключен' + (lis>1 ? ' ('+lis+')' : '')
   : lis === 0 ? '<span class="dot off"></span>поток жив, но OBS не подключен'
   : '<span class="dot on"></span>поток в эфире');

 // remaining < 0 означает тишину. Метаданные при этом ещё держат последний трек —
 // liquidsoap их не сбрасывает, и без этой проверки панель уверяла бы, что играет
 // то, что кончилось минуту назад.
 paused = !!d.paused;
 const pp = document.getElementById('pp');
 pp.textContent = paused ? 'Продолжить' : 'Пауза';
 pp.className = 'btn ' + (paused ? 'btn-primary' : 'btn-ghost');

 const silent = !(d.remaining > 0);
 const np = paused ? (d.now_playing || {}) : (silent ? {} : (d.now_playing || {}));
 document.getElementById('np').textContent = np.title
   ? (np.artist ? np.artist+' — '+np.title : np.title) : '— тишина —';
 document.getElementById('npby').textContent = np.title
   ? ('заказал ' + (np.requested_by||'—') +
      (paused ? ' · на паузе' : ' · осталось '+fmt(d.remaining))) : '';
 // Ширину полосы считаем от максимума, который видели у этого трека: плеер сообщает
 // только остаток, полной длительности играющего трека у него не спросить.
 if(d.remaining>0){ trackTotal=Math.max(trackTotal,d.remaining);
   document.getElementById('bar').style.width=(100-100*d.remaining/trackTotal)+'%'; }
 else { trackTotal=0; document.getElementById('bar').style.width='0'; }

 rewardOn = d.reward.enabled;
 const rw=document.getElementById('rw');
 rw.textContent = rewardOn ? 'Выключить' : 'Включить';
 rw.className = 'btn ' + (rewardOn ? 'btn-ghost' : 'btn-primary');
 document.getElementById('rwstate').textContent = rewardOn
   ? 'включена, видна зрителям · ' + d.reward.cost + ' баллов'
   : 'выключена — зрители её не видят';
 if(!dirty){
   document.getElementById('cost').value = d.settings.reward_cost;
   document.getElementById('maxdur').value = Math.round(d.settings.max_duration/60);
 }

 const q = d.queue || [];
 document.getElementById('empty').style.display = q.length ? 'none' : 'block';
 document.getElementById('tbl').innerHTML = q.map((it,i)=>
  '<tr><td class="num">'+(i+1)+'</td>'+
  '<td><div class="t">'+esc(it.title)+'</div>'+
  '<div class="meta">'+esc(it.uploader||'')+' · '+fmt(it.duration)+
  ' · '+esc(it.requested_by)+' · '+esc(it.source)+'</div></td>'+
  '<td style="text-align:right;white-space:nowrap">'+
  '<button class="btn btn-ghost btn-ic" title="Выше" onclick="act(\'up\',{id:\''+it.queue_id+'\'})">'+icon('up')+'</button> '+
  '<button class="btn btn-ghost btn-ic" title="Ниже" onclick="act(\'down\',{id:\''+it.queue_id+'\'})">'+icon('down')+'</button> '+
  '<button class="btn btn-danger btn-ic" title="Убрать" onclick="act(\'remove\',{id:\''+it.queue_id+'\'})">'+icon('x')+'</button>'+
  '</td></tr>').join('');
}
function esc(s){return String(s??'').replace(/[&<>"]/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;'}[c]));}
for(const id of ['cost','maxdur']){
  const el=document.getElementById(id);
  el.addEventListener('focus',()=>dirty=true);
  el.addEventListener('blur',()=>setTimeout(()=>dirty=false,1500));
}
document.getElementById('vol').addEventListener('mousedown',()=>volDragging=true);
document.getElementById('vol').addEventListener('touchstart',()=>volDragging=true);
load(); setInterval(load, 2000);
</script>`

// dockHTML — та же панель в раскладке под узкую колонку внутри OBS
// (View → Docks → Custom Browser Docks). Отличия от большой панели продиктованы
// шириной ~350px и тем, что док смотрят краем глаза во время эфира:
// крупная обложка текущего трека, компактный список, никаких настроек.
const dockHTML = `<!doctype html><meta charset="utf-8"><title>dev&gt;nulll music</title>
<meta name="viewport" content="width=device-width,initial-scale=1">
<style>{{BRAND}}
 body{padding:10px;font-size:14px}
 body::before,body::after{display:none} /* фоновые слои в узком доке только мешают */
 .top{display:flex;align-items:center;justify-content:space-between;margin-bottom:10px}
 .logo{font-family:var(--mono);font-size:13px;font-weight:700}
 .logo b{color:var(--primary)}
 .dot{width:7px;height:7px;border-radius:999px;display:inline-block;margin-right:6px}
 .dot.on{background:var(--ok)} .dot.off{background:var(--bad)}
 .now{display:flex;gap:10px;align-items:center;background:var(--surface);
      border:1px solid var(--border);border-radius:var(--radius);padding:10px;margin-bottom:8px}
 .cover{width:52px;height:52px;border-radius:6px;object-fit:cover;background:var(--surface-2);flex:none}
 .t{font-weight:600;line-height:1.25;font-size:13.5px;
    display:-webkit-box;-webkit-line-clamp:2;-webkit-box-orient:vertical;overflow:hidden}
 .meta{color:var(--text-muted);font-size:11.5px;font-family:var(--mono);margin-top:3px}
 .bar{height:2px;background:var(--surface-2);border-radius:2px;margin-top:8px;overflow:hidden}
 .bar i{display:block;height:100%;background:var(--primary);transition:width 1s linear}
 .ctl{display:flex;gap:6px;align-items:center;margin-bottom:10px}
 .ctl .btn{flex:none}
 input[type=range]{flex:1;accent-color:var(--primary);min-width:60px}
 .item{display:flex;gap:9px;align-items:center;padding:7px 0;border-top:1px solid var(--border)}
 .item img{width:38px;height:38px;border-radius:5px;object-fit:cover;background:var(--surface-2);flex:none}
 .x{display:flex;color:var(--text-muted);cursor:pointer;padding:5px;border-radius:6px;flex:none}
 .x:hover{color:var(--bad);background:var(--surface-2)}
 .empty{color:var(--text-muted);text-align:center;padding:18px 0;font-size:13px}
 .arr{display:flex;flex-direction:column;gap:3px}
 .arr span{display:flex;cursor:pointer;color:var(--text-muted)}
 .arr span:hover{color:var(--primary)}
 /* Награда переключается и отсюда: в эфире Азим смотрит в док, а не в браузер. */
 #rw{width:100%;justify-content:center}
 #rw[disabled]{opacity:.5;cursor:default}
 #msg{display:none;margin:8px 0 0;padding:7px 9px;border-radius:8px;font-size:12px;
      background:var(--surface-2);border:1px solid var(--border)}
 #msg.show{display:block} #msg.bad{border-color:var(--bad);color:var(--bad)}
</style>
{{ICONS}}
<div class="top">
 <div class="logo">dev<b>&gt;</b>nulll music</div>
 <div class="meta" id="alive"></div>
</div>

<div class="now">
 <img class="cover" id="cover" alt="">
 <div style="min-width:0;flex:1">
  <div class="t" id="np">— тишина —</div>
  <div class="meta" id="npby"></div>
  <div class="bar"><i id="bar" style="width:0"></i></div>
 </div>
</div>

<div class="ctl">
 <button class="btn btn-ghost btn-ic" id="pp" onclick="togglePause()" title="Пауза">
  <svg class="ic"><use href="#i-pause"/></svg></button>
 <button class="btn btn-ghost btn-ic" onclick="act('skip')" title="Пропустить">
  <svg class="ic"><use href="#i-skip"/></svg></button>
 <button class="btn btn-ghost btn-ic" onclick="mute()" id="mutebtn" title="Заглушить">
  <svg class="ic"><use href="#i-vol"/></svg></button>
 <input type="range" id="vol" min="0" max="150" value="100"
        oninput="volLabel(this.value)" onchange="setVol(this.value)" title="Громкость в эфире">
 <span class="meta" id="vollbl" style="width:34px;text-align:right">100%</span>
</div>

<div class="ctl" style="margin-top:-4px">
 <span class="meta" style="width:52px;display:flex;align-items:center;gap:5px" title="Только твои наушники">
  <svg class="ic ic-14"><use href="#i-head"/></svg>уши</span>
 <input type="range" id="mvol" min="0" max="150" value="100"
        oninput="mvolLabel(this.value)" onchange="setMVol(this.value)"
        title="Громкость только в наушниках Азима, зрителей не касается">
 <span class="meta" id="mvollbl" style="width:34px;text-align:right">100%</span>
</div>
<div class="meta" style="margin:-4px 0 10px;font-size:11px">
 верхний ползунок — что слышат зрители, нижний — только твои наушники
</div>

<div class="ctl">
 <button class="btn btn-sm btn-ghost" id="rw" onclick="toggleReward()"
         title="Включить или выключить у зрителей награду «Заказать трек»">…</button>
</div>
<div id="msg"></div>

<div id="list"></div>
<div class="empty" id="empty">Очередь треков пуста</div>

<script>
const BASE="{{BASE}}", KEY="{{KEY}}";
let lastVol=1, total=0, dragging=false, mdragging=false, dockPaused=false;
let rewardOn=false, rewardCost=0, rewardBusy=false;

function toast(t, bad){const m=document.getElementById('msg');m.textContent=t;
 m.className='show'+(bad?' bad':'');
 clearTimeout(window._t); window._t=setTimeout(()=>m.className='',4000);}

async function act(name, params={}){
 params.k=KEY;
 // Ошибку показываем: раньше док молчал, и неудачный переключатель награды
 // или отвалившийся плеер выглядели как «кнопка просто не работает».
 try{
  const r=await fetch(BASE+'/api/panel/'+name,{method:'POST',body:new URLSearchParams(params)});
  if(!r.ok){ const d=await r.json().catch(()=>({})); toast(d.error||'не вышло',true); }
 }catch(e){ toast('нет связи с сервером',true); }
 load();
}

// Награду переключаем и из дока: во время эфира Азим смотрит сюда, а не в браузер.
// Кнопка сразу показывает новое состояние, не дожидаясь ответа Twitch, — иначе на
// канале до Сиднея это выглядит как зависший клик.
async function toggleReward(){
 if(rewardBusy) return;
 rewardBusy=true;
 const want=!rewardOn;
 rewardOn=want; paintReward(true);
 await act('settings',{reward_enabled:want.toString()});
 rewardBusy=false;
}

function icon(name, cls){return '<svg class="ic'+(cls?' '+cls:'')+'"><use href="#i-'+name+'"/></svg>';}
function setIcon(id, name){document.getElementById(id).querySelector('use').setAttribute('href','#i-'+name);}

function paintReward(pending){
 const b=document.getElementById('rw');
 b.innerHTML=icon('gift','ic-14')+'<span>'+(rewardOn?'заказы включены':'заказы выключены')
   +(rewardOn&&rewardCost?' · '+rewardCost:'')+(pending?' …':'')+'</span>';
 b.className='btn btn-sm '+(rewardOn?'btn-ghost':'btn-primary');
 b.disabled=!!pending;
}
function volLabel(v){document.getElementById('vollbl').textContent=v+'%';}
function setVol(v){dragging=false;act('volume',{v:String(v/100)});}
function mvolLabel(v){document.getElementById('mvollbl').textContent=v+'%';}
function setMVol(v){mdragging=false;act('volume_monitor',{v:String(v/100)});}
function togglePause(){act(dockPaused?'resume':'pause');}
function mute(){const cur=Number(document.getElementById('vol').value);
 const next = cur>0 ? 0 : Math.round(lastVol*100)||100;
 if(cur>0) lastVol=cur/100;
 document.getElementById('vol').value=next; volLabel(next); setVol(next);}
function fmt(s){s=Math.max(0,Math.round(s));return Math.floor(s/60)+':'+String(s%60).padStart(2,'0');}
function esc(s){return String(s??'').replace(/[&<>"]/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;'}[c]));}

async function load(){
 let d; try{ d=await (await fetch(BASE+'/api/panel/state?k='+KEY)).json(); }catch(e){ return; }
 if(d.error) return;
 const lis=(typeof d.listeners==='number')?d.listeners:-1;
 document.getElementById('alive').innerHTML = !d.alive ? '<span class="dot off"></span>нет потока'
   : (lis>0 ? '<span class="dot on"></span>OBS подключен'
   : lis===0 ? '<span class="dot off"></span>OBS не подключен'
   : '<span class="dot on"></span>в эфире');

 dockPaused = !!d.paused;
 setIcon('pp', dockPaused ? 'play' : 'pause');

 // Пока запрос на переключение в полёте, состояние с сервера не перетираем:
 // иначе поллинг вернул бы старое значение и кнопка мигнула бы назад.
 if(!rewardBusy && d.reward){
   rewardOn=!!d.reward.enabled; rewardCost=d.reward.cost||0; paintReward(false);
 }

 // remaining < 0 — тишина; метаданные последнего трека liquidsoap не сбрасывает.
 const silent = !(d.remaining > 0) && !dockPaused;
 const np = silent ? {} : (d.now_playing||{}), q = d.queue||[];
 document.getElementById('np').textContent = np.title || '— тишина —';
 document.getElementById('npby').textContent = np.title
   ? (np.requested_by||'') + (dockPaused ? ' · пауза' : ' · '+fmt(d.remaining)) : '';
 // Обложку текущего трека берём из очереди по совпадению названия: плеер картинок
 // не хранит, а тащить их в метаданные потока избыточно.
 const cover=document.getElementById('cover');
 const known=(window._covers||{})[np.title];
 cover.style.visibility = known ? 'visible' : 'hidden';
 if(known) cover.src=known;

 if(d.remaining>0){ total=Math.max(total,d.remaining);
   document.getElementById('bar').style.width=(100-100*d.remaining/total)+'%'; }
 else { total=0; document.getElementById('bar').style.width='0'; }

 if(!dragging && typeof d.volume==='number'){
   const v=Math.round(d.volume*100);
   document.getElementById('vol').value=v; volLabel(v);
   setIcon('mutebtn', v>0 ? 'vol' : 'mute');
 }
 if(!mdragging && typeof d.monitor_volume==='number'){
   const mv=Math.round(d.monitor_volume*100);
   document.getElementById('mvol').value=mv; mvolLabel(mv);
 }

 window._covers=window._covers||{};
 q.forEach(it=>{ if(it.thumb) window._covers[it.title]=it.thumb; });

 document.getElementById('empty').style.display=q.length?'none':'block';
 document.getElementById('list').innerHTML=q.map(it=>
  '<div class="item">'+
   (it.thumb?'<img src="'+esc(it.thumb)+'" alt="">':'<img alt="">')+
   '<div style="min-width:0;flex:1"><div class="t">'+esc(it.title)+'</div>'+
   '<div class="meta">'+esc(it.source)+' · '+fmt(it.duration)+' · '+esc(it.requested_by)+'</div></div>'+
   '<div class="arr"><span title="Выше" onclick="act(\'up\',{id:\''+it.queue_id+'\'})">'+icon('up','ic-14')+'</span>'+
   '<span title="Ниже" onclick="act(\'down\',{id:\''+it.queue_id+'\'})">'+icon('down','ic-14')+'</span></div>'+
   '<div class="x" title="Убрать" onclick="act(\'remove\',{id:\''+it.queue_id+'\'})">'+icon('x','ic-14')+'</div>'+
  '</div>').join('');
}
document.getElementById('vol').addEventListener('mousedown',()=>dragging=true);
document.getElementById('mvol').addEventListener('mousedown',()=>mdragging=true);
load(); setInterval(load,2000);
</script>`
