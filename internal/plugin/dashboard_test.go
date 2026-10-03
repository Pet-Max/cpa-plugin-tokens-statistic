package plugin

import (
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

func TestDashboardUsesBoundedSafeRendering(t *testing.T) {
	html := dashboardHTML
	for _, required := range []string{
		"document.createDocumentFragment()",
		"AbortController",
		"setTimeout(function(){controller.abort();},timeout)",
		"series.length>240",
		"body.replaceChildren(fragment)",
		"getElementById('analysisPlot').replaceChildren(fragment)",
		"var hitLayer=document.getElementById('analysisHitLayer');hitLayer.setAttribute('clip-path','url(#analysisClip)');if(!precomputed)hitLayer.replaceChildren(hitFragment)",
		"var resourceBase=publicPathPrefix+'/v0/resource/plugins/'",
		"var statsURL=resourceBase+'/stats'",
		"load(true).catch(function(error)",
		"window.parent.document.documentElement",
		"new MutationObserver",
		"attributeFilter:['data-theme','style','class','lang']",
		"initializeThemeSync()",
		"window.matchMedia",
		"supportedLocales=['en','zh-CN','zh-TW','ru']",
		"function detectLocale()",
		"function normalizeLocale(value)",
		"navigator.languages",
		"window.addEventListener('languagechange'",
		"document.documentElement.lang=locale",
		"formatterLocale=locale==='zh-CN'?'zh-CN':locale==='zh-TW'?'zh-TW':locale==='ru'?'ru-RU':'en-US'",
		"function translateStatic()",
		"function localeNumber(value,options)",
		"function localeDate(value,options)",
		"<html lang=\"zh-CN\" data-theme=\"dark\" style=\"background:#151412;color-scheme:dark\">",
		"<meta name=\"color-scheme\" content=\"dark light\">",
		"<style id=\"initial-theme\">",
		"html{background:#151412;color-scheme:dark}",
		"html:not([data-theme]){background:#faf9f5;color-scheme:light}",
		"html[data-theme='white']{background:#fff;color-scheme:light}",
		"html[data-theme='dark']{background:#151412;color-scheme:dark}",
		"var theme='dark',background='#151412';",
		"getComputedStyle(parentRoot).getPropertyValue('--bg-secondary')",
		"window.frameElement.style.backgroundColor=background",
	} {
		if !strings.Contains(html, required) {
			t.Fatalf("dashboard missing %q", required)
		}
	}
	for _, forbidden := range []string{
		`updated_at:base.updated_at||''`,
		"replaceChildren.apply",
		"Math.max.apply",
		"localStorage",
		"sessionStorage",
		"data-theme-value",
		"themePopover",
		"connectButton",
		"logoutButton",
		"innerHTML",
		"column-hide-button",
		"data-hide-column",
		"data-hide-dimension-column",
		"row.hidden=true",
		`preserveAspectRatio="none"`,
		"fetch('stats')",
		`fetch("stats")`,
		`costFor(name,input,output)`,
		`fetch('https://models.dev`,
		`fetch("https://models.dev`,
		`fetch('https://open.er-api.com`,
		`fetch("https://open.er-api.com`,
	} {
		if strings.Contains(html, forbidden) {
			t.Fatalf("dashboard contains unsafe pattern %q", forbidden)
		}
	}
}

func TestDashboardColumnMenusCanOverflowShortTables(t *testing.T) {
	html := dashboardHTML
	for _, required := range []string{
		`.panel{overflow:hidden;min-width:0}`,
		`.table-panel{overflow:visible}`,
		`.table-wrap{max-height:540px;overflow:auto;border-radius:0 0 12px 12px;scrollbar-gutter:stable}`,
		`.request-columns-menu{position:absolute`,
		`max-height:calc(100dvh - 32px);overflow:auto`,
		`function positionColumnsMenu(menu,button)`,
		`menu.style.position='fixed'`,
		`window.addEventListener('resize',function(){if(!document.getElementById('requestColumnsMenu').hidden)positionColumnsMenu`,
	} {
		if !strings.Contains(html, required) {
			t.Fatalf("dashboard missing short-table column menu fix %q", required)
		}
	}
}

func TestDashboardAnimatesEveryDropdownMenu(t *testing.T) {
	html := dashboardHTML
	for _, required := range []string{
		`.dropdown-surface{opacity:0;pointer-events:none;transform:translateY(-6px) scale(.98)`,
		`.dropdown-surface.is-open{opacity:1;pointer-events:auto;transform:translateY(0) scale(1)}`,
		`.dropdown-surface.is-closing{transition-duration:120ms}`,
		`function openDropdownSurface(menu,button,position)`,
		`function closeDropdownSurface(menu,button,restoreFocus,afterClose)`,
		`event.propertyName==='opacity'`,
		`dropdownCloseTimers.set(menu,setTimeout`,
		`menu.classList.add('is-opening')`,
		`menu.classList.add('is-closing')`,
		`@media(prefers-reduced-motion:reduce)`,
	} {
		if !strings.Contains(html, required) {
			t.Fatalf("dashboard missing dropdown animation contract %q", required)
		}
	}
}

func TestDashboardEnhancesNativeSelectMenus(t *testing.T) {
	html := dashboardHTML
	for _, required := range []string{
		`function enhanceSelect(select)`,
		`function syncEnhancedSelect(select)`,
		`function enhanceDashboardSelects(root)`,
		`role='combobox'`,
		`role='listbox'`,
		`role='option'`,
		`aria-activedescendant`,
		`new Event('change',{bubbles:true})`,
		`['ArrowDown','ArrowUp','Home','End']`,
		`enhanceSelect(select);renderSourceOptions([]);`,
		`syncEnhancedSelect(select)`,
		`enhanceDashboardSelects(list)`,
		`enhanceDashboardSelects(document)`,
	} {
		if !strings.Contains(html, required) {
			t.Fatalf("dashboard missing enhanced-select contract %q", required)
		}
	}
}

func TestDashboardIncludesInteractiveAnalyticsFeatures(t *testing.T) {
	html := fullDashboardHTML
	for _, required := range []string{
		`id="granularity"`,
		`id="tokenUnitButton"`,
		`var exchangeRateURL=resourceBase+'/exchange-rate'`,
		`function formatTokenTotal(value)`,
		`function toggleTokenUnit()`,
		`B:{suffix:'B',divisor:1e9}`,
		`k:{suffix:'K',divisor:1e3}`,
		`order=['full','k','m','B']`,
		`token_display_mode:tokenDisplayMode`,
		`params.set('token_display_mode',value.token_display_mode)`,
		`scheduleDashboardPreferencesSave();`,
		`tokenDisplayModes[value.token_display_mode]?value.token_display_mode:'full'`,
		`updateTokenUnitButton();updateRangeButton()`,
		`id="avgTTFT"`,
		`id="avgLatency"`,
		`id="totalCalls"`,
		`id="successCalls"`,
		`id="successRate"`,
		`id="failedCalls"`,
		`id="failureRate"`,
		`id="totalTokens"`,
		`id="inputTokens"`,
		`id="outputTokens"`,
		`id="cacheReadTokens"`,
		`id="cacheRate"`,
		`function secondsText(value)`,
		`function rateText(rate)`,
		`function cacheKindSums(groups)`,
		`function trendCacheReadTokens(point)`,
		`bucket.cacheRead+=readTokens`,
		`item.cacheRate=item.denominator>0?Math.min(100,item.read/item.denominator*100):null;`,
		`item.ttft=item.ttftSamples>0?item.ttftTotal/item.ttftSamples/1e9:null;`,
		`item.latency=item.latencySamples>0?item.latencyTotal/item.latencySamples/1e9:null;`,
		`var analysisMetrics=[`,
		`function analysisSeriesLabel(key)`,
		`function analysisMetricValue(metric,point)`,
		`function analysisModelKind(name)`,
		`function analysisVisibleMetrics()`,
		`function aggregateAnalysis()`,
		`function renderAnalysis(precomputed)`,
		`function showAnalysisTooltip(event,point)`,
		`function smoothPath(points)`,
		`model_series`,
		`function selectModel(name,options)`,
		`function modelCell(row,group)`,
		`addEventListener('wheel'`,
		`moneyFormatters[key]`,
		`var costsURL=resourceBase+'/costs'`,
		`function visibleCostSummary()`,
		`priceEditCacheRead`,
		`priceEditCacheWrite`,
		`function renderPriceTable()`,
		`function openPriceEditDialog(name)`,
		`item.estimated_cost`,
		`record.estimated_cost`,
		`estimated.input_usd`,
		`estimated.output_usd`,
		`estimated.cache_read_usd`,
		`estimated.cache_creation_usd`,
		`estimated.total_usd`,
		`async function exportCSV()`,
		`function exportPNG()`,
		`id="exportBackup"`,
		`var backupURL=resourceBase+'/full-mode/backup'`,
		`var restoreURL=resourceBase+'/full-mode/restore'`,
		`async function downloadBackup()`,
		`function restoreBackup()`,
		`async function confirmAndRestore(file)`,
		`if(file.size > 64*1024*1024){text('error',t('backup.fileTooLarge'));return;}`,
		`selectedSource=''`,
		`renderSourceOptions([])`,
		`await loadDashboardPreferences()`,
		`function restoreBackup(){
  closeExportMenu();`,
		`data-i18n="button.downloadBackup"`,
		`data-i18n="button.restoreBackup"`,
		`该时间段内暂无调用记录`,
		`grid-template-columns:repeat(6`,
		`grid-template-columns:repeat(2`,
		`id="granularity" class="control granularity-select"`,
		`<option value="minute" data-i18n="granularity.minute">`,
		`<option value="hour" selected data-i18n="granularity.hour">`,
		`id="analysisChart"`,
		`id="analysisWrap"`,
		`id="analysisZoomInButton"`,
		`id="analysisZoomOutButton"`,
		`id="analysisResetZoomButton"`,
		`data-i18n="analysis.title"`,
		`function chartMetrics(svg,fallbackHeight)`,
		`function initializeChartResize()`,
		`new ResizeObserver`,
		`svg.setAttribute('viewBox','0 0 '+width+' '+height)`,
		`.bar-hit:focus-visible`,
		`Math.floor(plotW/85)`,
		`id="requestRows"`,
		`var requestsURL=resourceBase+'/requests'`,
		`async function loadRequests()`,
		`id="requestPrev"`,
		`id="requestNext"`,
		`id="requestPageSize"`,
		`id="requestModelFilter"`,
		`id="requestSourceFilter"`,
		`id="requestResultFilter"`,
		`function renderRequestFilters()`,
		`requestSourceFilter=''`,
		`params.set('model',requestModelFilter)`,
		`params.set('source',requestSourceFilter)`,
		`params.set('result',requestResultFilter)`,
		`requestLimit=Math.max(1,Math.min(500`,
		`id="requestColumnsButton"`,
		`id="requestColumnsMenu"`,
		`id="requestHeaders"`,
		`function requestTime(value)`,
		`second:'2-digit'`,
		`var requestColumns=[`,
		`sortButton.dataset.requestSort=column.key`,
		`function sortedRequestItems(items)`,
		`hiddenRequestColumns=new Set()`,
		`id="dimensionPrev"`,
		`id="dimensionNext"`,
		`id="dimensionPageSize"`,
		`dimensionLimit=Math.max(1,Math.min(500`,
		`id="dimensionColumnsButton"`,
		`id="dimensionColumnsMenu"`,
		`id="dimensionHeaders"`,
		`var dimensionColumns=[`,
		`sortButton.dataset.dimensionSort=column.key`,
		`function sortedDimensionGroups(groups)`,
		`hiddenDimensionColumns=new Set()`,
		`var preferencesURL=resourceBase+'/preferences'`,
		`function dashboardPreferencesPayload()`,
		`function applyDashboardPreferences(value)`,
		`async function loadDashboardPreferences()`,
		`function dashboardPreferencesSaveURL()`,
		`async function saveDashboardPreferences()`,
		`function scheduleDashboardPreferencesSave()`,
		`hidden_request_columns:Array.from(hiddenRequestColumns)`,
		`hidden_dimension_columns:Array.from(hiddenDimensionColumns)`,
		`time_range_mode:appliedRangeMode`,
		`params.set('time_range_mode',value.time_range_mode)`,
		`params.set('save','1')`,
		`params.append('hidden_request_column',key)`,
		`params.append('hidden_dimension_column',key)`,
		`keepalive:true`,
		`window.addEventListener('pagehide'`,
		`loadDashboardPreferences().catch(function(error)`,
		`function loadGroups(sequence,query)`,
		`var statsGroupsURL=resourceBase+'/stats/groups'`,
		`params.set('offset',String(dimensionOffset))`,
		`params.set('sort',dimensionSortKey)`,
		`renderGroups(page.items,Number(page.total||0))`,
		`empty.colSpan=Math.max(1,columns.length)`,
		`function zoomTrend(points,zoom,factor,anchorRatio,render)`,
		`{passive:false,capture:true}`,
		`首字`,
		`缓存命中`,
	} {
		if !strings.Contains(html, required) {
			t.Fatalf("dashboard missing analytics feature %q", required)
		}
	}
}

func TestDashboardCacheRateUsesAccountingAwareDenominator(t *testing.T) {
	html := dashboardHTML
	for _, required := range []string{
		`function trendCacheReadTokens(point){var cacheRead=Number(point.cache_read_tokens||0);return cacheRead>0?cacheRead:Number(point.cached_tokens||0);}`,
		`var kind=hasModelSplit?analysisModelKind(name):'subset';`,
		`bucket.denominator+=kind==='independent'?inputTokens+creationTokens+readTokens:inputTokens;`,
		`item.cacheRate=item.denominator>0?Math.min(100,item.read/item.denominator*100):null;`,
		`function cacheKindSums(groups){`,
		`kindSums.read/kindSums.denominator*100`,
		`function analysisModelKind(name){var rows=modelRows();for(var i=0;i<rows.length;i++){if(modelName(rows[i].model)===name)return cacheAccountingKind(rows[i]);}return 'subset';}`,
	} {
		if !strings.Contains(html, required) {
			t.Fatalf("dashboard missing accounting-aware cache rate contract %q", required)
		}
	}
}

func TestDashboardAnalysisKeepsContinuousTimeBuckets(t *testing.T) {
	html := dashboardHTML
	for _, required := range []string{
		`function nextBucketInfo(info,granularity)`,
		`function continuousBuckets(map,granularity,emptyBucket)`,
		`var range=resolvedDateRange(),start=bucketInfo(range.start,granularity),end=bucketInfo(new Date(range.end.getTime()-1),granularity)`,
		`while(info.stamp<=end.stamp&&steps<100000)`,
		`function emptyAnalysisBucket(info){return {key:info.key,label:info.label,stamp:info.stamp,hasData:false`,
		`if(!point.hasData){tooltipRow(t('chart.noRequests'),t('locale.unavailable'));`,
		`return continuousBuckets(map,granularity,emptyAnalysisBucket)`,
	} {
		if !strings.Contains(html, required) {
			t.Fatalf("dashboard missing continuous time-series behavior %q", required)
		}
	}
	if strings.Contains(html, `return capSeries(result);`) {
		t.Fatal("continuous time buckets must not be downsampled because that would skip empty intervals")
	}
}

func TestDashboardRefreshPreservesTrendZoom(t *testing.T) {
	html := dashboardHTML
	if !strings.Contains(html, "function startTimer(){if(refreshTimer)clearInterval(refreshTimer);refreshTimer=setInterval(function(){load().catch") {
		t.Fatal("dashboard must retain its automatic refresh path")
	}
	if strings.Contains(html, "if(costLoadError)text('error',t('error.costUnavailable',{message:costLoadError}));resetTrendZooms();renderRequestFilters()") {
		t.Fatal("dashboard render must not reset either trend zoom during a refresh")
	}
	for _, required := range []string{
		"function confirmDateRange(){",
		"updateGranularityForRange();resetTrendZooms();",
		"document.getElementById('granularity').addEventListener('change',function(){resetTrendZooms();renderVisuals();})",
	} {
		if !strings.Contains(html, required) {
			t.Fatalf("dashboard must still reset both trend zooms when their data scope changes: %q", required)
		}
	}
}

func TestDashboardRefreshRetainsMatchingCostSnapshot(t *testing.T) {
	html := dashboardHTML
	for _, required := range []string{
		"currentCosts=null,costQuery='',costLoadError=''",
		"render(initial,costQuery===query?currentCosts:null)",
		"costQuery=query;costLoadError='';render(currentData,costs);",
		"costLoadError=error.message;render(currentData,costQuery===query?currentCosts:null);",
	} {
		if !strings.Contains(html, required) {
			t.Fatalf("dashboard refresh must retain matching cost data until its replacement arrives: %q", required)
		}
	}
}

func TestDashboardRefreshKeepsCostSnapshotDuringRollingRangeRefresh(t *testing.T) {
	html := dashboardHTML
	for _, required := range []string{
		"function clearErrors(){text('error','');costLoadError='';}",
		"var costLoading=false,baseRender=render,baseRenderVisuals=renderVisuals",
		"if(!costs&&currentCosts){costLoading=!costLoadError;costs=currentCosts;}",
		"t('status.costRefreshing')",
		"t('status.costRefreshFailed')",
	} {
		if !strings.Contains(html, required) {
			t.Fatalf("dashboard must retain the previous cost snapshot during refresh: %q", required)
		}
	}
	if strings.Contains(html, "currentCosts=null,costQuery='',costLoadError='',costLoading") {
		t.Fatal("dashboard cost state must not clear the cost snapshot on refresh")
	}
}

func TestDashboardAnalysisZoomContract(t *testing.T) {
	html := dashboardHTML
	for _, required := range []string{
		"analysisZoom={start:0,size:0},lastAnalysis=[]",
		"function resetTrendZooms(){resetTrendZoom(analysisZoom);}",
		"function renderAnalysis(precomputed){var svg=document.getElementById('analysisChart');if(!svg)return;",
		"all=precomputed||aggregateAnalysis(),series=visibleTrend(all,analysisZoom);lastAnalysis=all;",
		`id="analysisZoomOutButton"`,
		`id="analysisZoomInButton"`,
		`id="analysisResetZoomButton"`,
		`id="analysisWrap"`,
		"document.getElementById('analysisZoomInButton').addEventListener('click',function(){zoomTrend(lastAnalysis,analysisZoom,.7,.5,renderAnalysis);})",
		"document.getElementById('analysisZoomOutButton').addEventListener('click',function(){zoomTrend(lastAnalysis,analysisZoom,1/.7,.5,renderAnalysis);})",
		"document.getElementById('analysisResetZoomButton').addEventListener('click',function(){analysisZoom.start=0;analysisZoom.size=lastAnalysis.length;renderAnalysis();})",
		"document.getElementById('analysisWrap').addEventListener('wheel',function(event){if(lastAnalysis.length<2)return;",
		"zoomTrend(lastAnalysis,analysisZoom,factor,ratio,function(){});scheduleAnalysisRender();",
		"function scheduleAnalysisRender(){if(analysisWheelFrame)return;analysisWheelFrame=requestAnimationFrame(function(){analysisWheelFrame=0;renderAnalysis();});}",
	} {
		if !strings.Contains(html, required) {
			t.Fatalf("dashboard missing analysis zoom contract %q", required)
		}
	}
	for _, forbidden := range []string{"barZoomStart", "barZoomSize", "tokenTrendZoom", "costTrendZoom"} {
		if strings.Contains(html, forbidden) {
			t.Fatalf("dashboard must not retain stale trend zoom state %q", forbidden)
		}
	}
}

func TestDashboardRequestCacheHitRateContract(t *testing.T) {
	html := dashboardHTML
	for _, required := range []string{
		`{key:'cache_hit_rate',label:'table.cacheHitRate',numeric:true}`,
		`function cacheAccountingKind(point)`,
		`function cacheHitRateForPoint(point)`,
		`denominator=input+creation+read`,
		`return cacheHitRate(cacheReadTokens(point),cacheHitDenominator(point))`,
		`case 'cache_hit_rate':return cacheHitRateForPoint(item);`,
		`case 'cache_hit_rate':td=cell(row,formatCacheHitRate(cacheHitRateForPoint(item)),'num');break;`,
		`t('value.unavailable')`,
		`localeNumber(rate,{minimumFractionDigits:2,maximumFractionDigits:2})+'%'`,
	} {
		if !strings.Contains(html, required) {
			t.Fatalf("dashboard missing request cache hit rate contract %q", required)
		}
	}
}

func TestDashboardTableColumnAlignmentContract(t *testing.T) {
	html := dashboardHTML
	for _, required := range []string{
		// centered columns: request details
		`{key:'service_tier',label:'table.tier',center:true},`,
		`{key:'result',label:'table.result',center:true},`,
		`{key:'cache_hit',label:'table.cacheHit',center:true},`,
		// centered columns: both tables share this definition
		`{key:'reasoning_effort',label:'table.reasoningEffort',center:true},`,
		`{key:'price_source',label:'table.priceSource',center:true}`,
		// centered columns: model details
		`{key:'executor_type',label:'table.executor',center:true},`,
		`{key:'auth_type',label:'table.authType',center:true},`,
		`{key:'service_tier',label:'table.serviceTier',center:true},`,
		"{key:'price_source',label:'table.priceSource',center:true}\n];",
		// alignment CSS: empty sort placeholder collapses, center class aligns header and body
		`td.center,th.center{text-align:center}`,
		`th.center .request-sort-button{margin:0 auto;text-align:center}`,
		`.sort-indicator:empty{display:none}`,
		`if(column.numeric)th.className='num';if(column.center)th.className='center';`,
		`if(td&&column.center)td.classList.add('center');if(td)td.dataset.column=column.key;return td;`,
	} {
		if !strings.Contains(html, required) {
			t.Fatalf("dashboard missing table column alignment contract %q", required)
		}
	}
}

func TestDashboardDimensionCostSortContract(t *testing.T) {
	html := dashboardHTML
	for _, required := range []string{
		`{key:'estimated_cost',label:'table.estimatedCost',numeric:true},`,
		`{key:'price_source',label:'table.priceSource',center:true}`,
		`'average_ttft_ns','estimated_cost'].indexOf(key)>=0?'desc':'asc'`,
	} {
		if !strings.Contains(html, required) {
			t.Fatalf("dashboard missing dimension cost sort contract %q", required)
		}
	}
	if strings.Contains(html, ",sortable:false") {
		t.Fatal("dashboard still has unsortable dimension columns; estimated_cost and price_source must be sortable")
	}
}

func TestDashboardAnalysisSeriesRenderContract(t *testing.T) {
	publicHTML := dashboardHTML
	for _, required := range []string{
		// requests render as bottom bars on their own scale
		`if(metric.key==='requests'){var barBase=top+plotH,barZone=plotH*0.2,barStride=Math.max(1,Math.ceil(3/slot));`,
		`c1y=Math.max(lo,Math.min(hi,p1.y+(p2.y-p0.y)/6)),c2y=Math.max(lo,Math.min(hi,p2.y-(p3.y-p1.y)/6))`,
		`svgNode('rect',{x:left,y:top-2,width:plotW,height:plotH+4})`,
		`.analysis-bar{fill:var(--metric-requests);opacity:.85}`,
		// token series: straight 2px lines; durations keep smooth 2.5px; ratios stay dashed
		`function linePath(points)`,
		`class:'analysis-line'+(metric.kind==='percent'?' percent':(metric.kind==='count'?' count':''))`,
		`.analysis-line.count{stroke-width:2}`,
		// latency recolored to violet in both themes
		`--metric-latency:#8b5cf6`,
		`--metric-latency:#a78bfa`,
	} {
		if !strings.Contains(publicHTML, required) {
			t.Fatalf("dashboard missing analysis series render contract %q", required)
		}
	}
	if strings.Contains(publicHTML, "'fill-opacity'") {
		t.Fatal("analysis chart still renders area fills; token series must be plain lines")
	}
	// the PNG export keeps a canvas copy of the same design (full variant only:
	// the public variant strips the export script section)
	for _, required := range []string{
		`if(metric.key==='requests'){var barZone=chartH*0.2;`,
		`ctx.lineWidth=metric.kind==='percent'?2:(metric.kind==='count'?2:2.5);`,
	} {
		if !strings.Contains(fullDashboardHTML, required) {
			t.Fatalf("full dashboard missing analysis export render contract %q", required)
		}
	}
	if strings.Contains(fullDashboardHTML, "'fill-opacity'") {
		t.Fatal("analysis PNG export still renders area fills; token series must be plain lines")
	}
}

func TestDashboardAnalysisContinuousWindowContract(t *testing.T) {
	publicHTML := dashboardHTML
	for _, required := range []string{
		// fractional window: slice spans partial edge buckets
		`return points.slice(Math.max(0,Math.floor(zoom.start)),Math.min(points.length,Math.ceil(zoom.start+zoom.size)));`,
		`var windowStart=analysisZoom.size?analysisZoom.start:0,windowSize=analysisZoom.size||all.length,absBase=Math.floor(windowStart),slot=plotW/windowSize,`,
		`function xAt(index){return series.length>1?left+(absBase+index-windowStart)*slot+slot/2:left+plotW/2;}`,
		// clip data series to the plot area; partial edge buckets lose their x label
		`var plotClip=svgNode('clipPath',{id:'analysisClip'});`,
		`var clippedSeries=svgNode('g',{'clip-path':'url(#analysisClip)'});`,
		`var bucketLeft=absBase+index-windowStart;if(bucketLeft<0||bucketLeft+1>windowSize)return;`,
		// drag, track click, zoom anchor and pan step are continuous (no bucket rounding)
		`var next=clampStart(drag.start+(event.clientX-drag.x)/rect.width*lastAnalysis.length);`,
		`target=clampStart((event.clientX-rect.left)/rect.width*lastAnalysis.length-visible/2);`,
		`zoom.start=anchor-ratio*next;`,
		`var current=zoom.size||points.length,step=current*.12;`,
		// drag renders are throttled to ~30fps and dense windows downsample bars/hit zones
		`barStride=Math.max(1,Math.ceil(3/slot));`,
		`var hitStride=Math.max(1,Math.ceil(4/slot));`,
		`select.value=duration<6*60*60*1000?'minute':'hour';`,
	} {
		if !strings.Contains(publicHTML, required) {
			t.Fatalf("dashboard missing analysis continuous window contract %q", required)
		}
	}
	// PNG export mirrors the continuous window with canvas clipping
	for _, required := range []string{
		`var windowStart=analysisZoom.size?analysisZoom.start:0,windowSize=analysisZoom.size||data.length,absBase=Math.floor(windowStart),slot=chartW/windowSize;`,
		`ctx.rect(chartX,chartY-2,chartW,chartH+4);ctx.clip();`,
	} {
		if !strings.Contains(fullDashboardHTML, required) {
			t.Fatalf("full dashboard missing analysis export continuous window contract %q", required)
		}
	}
}

func TestDashboardRequestCacheHitRateExecution(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not available")
	}
	start := strings.Index(dashboardHTML, "function cacheCounter(point,key)")
	end := strings.Index(dashboardHTML, "function renderRequestHeaders()")
	if start < 0 || end < start {
		t.Fatal("dashboard cache-hit helpers are missing")
	}
	script := `
var requestSortKey='cache_hit_rate',requestSortDirection='asc',formatterLocale='en-US';
` + dashboardHTML[start:end] + `
function equal(actual,expected,label){if(actual!==expected)throw new Error(label+': expected '+expected+', got '+actual);}
function rate(point){return cacheHitRateForPoint(point);}
var creationOnly=[
  ['openai',{provider:'openai',input_tokens:100,cache_read_tokens:0,cache_creation_tokens:10,cached_tokens:10}],
  ['claude',{provider:'claude',input_tokens:10,cache_read_tokens:0,cache_creation_tokens:10,cached_tokens:10}],
  ['gemini',{provider:'gemini',input_tokens:100,cache_read_tokens:0,cache_creation_tokens:10,cached_tokens:10}]
];
creationOnly.forEach(function(example){equal(rate(example[1]),0,example[0]+' creation-only');});
equal(cacheReadTokens({provider:'unknown',input_tokens:100,cache_read_tokens:0,cache_creation_tokens:10,cached_tokens:10}),0,'unknown creation-only cache read');
equal(rate({provider:'openai',input_tokens:100,cache_read_tokens:30,cache_creation_tokens:0,cached_tokens:0}),30,'subset read-only');
equal(rate({provider:'gemini',input_tokens:100,cache_read_tokens:30,cache_creation_tokens:0,cached_tokens:0}),30,'separate read-only');
equal(rate({provider:'gemini',input_tokens:100,cache_read_tokens:30,cache_creation_tokens:10,cached_tokens:0}),30,'separate read plus creation');
equal(rate({provider:'claude',input_tokens:10,cache_read_tokens:80,cache_creation_tokens:10,cached_tokens:0}),80,'independent read plus creation');
equal(rate({provider:'unknown',input_tokens:100,cache_read_tokens:30,cache_creation_tokens:0,cached_tokens:0}),null,'unknown provider');
var items=[
  {sequence:1,provider:'openai',input_tokens:100,cache_read_tokens:25,cache_creation_tokens:0,cached_tokens:0},
  {sequence:2,provider:'unknown',input_tokens:100,cache_read_tokens:25,cache_creation_tokens:0,cached_tokens:0},
  {sequence:3,provider:'openai',input_tokens:100,cache_read_tokens:50,cache_creation_tokens:0,cached_tokens:0}
];
equal(sortedRequestItems(items).map(function(item){return item.sequence;}).join(','),'1,3,2','ascending null last');
requestSortDirection='desc';
equal(sortedRequestItems(items).map(function(item){return item.sequence;}).join(','),'3,1,2','descending null last');
`
	command := exec.Command(node, "--check", "-")
	command.Stdin = strings.NewReader(script)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("dashboard cache-hit test script syntax: %v\n%s", err, output)
	}
	command = exec.Command(node, "-")
	command.Stdin = strings.NewReader(script)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("dashboard cache-hit execution: %v\n%s", err, output)
	}
}

func TestDashboardCardCacheRateExecution(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not available")
	}
	start := strings.Index(dashboardHTML, "function cacheCounter(point,key)")
	end := strings.Index(dashboardHTML, "function renderRequestHeaders()")
	cardStart := strings.Index(dashboardHTML, "function secondsText(value)")
	cardMid := strings.Index(dashboardHTML, "function cacheKindSums(groups)")
	cardEnd := strings.Index(dashboardHTML, "function modelCell(")
	if start < 0 || end < start || cardStart < 0 || cardEnd < cardStart {
		t.Fatal("dashboard cache-rate helpers are missing")
	}
	script := `
var requestSortKey='time',requestSortDirection='desc',formatterLocale='en-US';
function localeNumber(value,options){return new Intl.NumberFormat(formatterLocale,options).format(Number(value||0));}
` + dashboardHTML[start:end] + `
` + dashboardHTML[cardStart:cardMid] + `
` + dashboardHTML[cardMid:cardEnd] + `
function equal(actual,expected,label){if(actual!==expected)throw new Error(label+': expected '+expected+', got '+actual);}
var sums=cacheKindSums([
 {model:'claude-sonnet-4-5',provider:'claude',executor_type:'anthropic',input_tokens:100,cache_read_tokens:80,cache_creation_tokens:10,cached_tokens:0},
 {model:'gpt-5.2',provider:'openai',executor_type:'openaicompatexecutor',input_tokens:200,cache_read_tokens:50,cache_creation_tokens:0,cached_tokens:50},
 {model:'mystery',input_tokens:10,cache_read_tokens:10,total_tokens:20}
]);
equal(sums.read,130,'cache read sum');
equal(sums.denominator,390,'denominator sums input+creation+read for independent models and input for subset models, skipping unknown providers');
equal(cacheKindSums([{model:'x',provider:'unknown',input_tokens:10,cache_read_tokens:5}]).denominator,0,'unknown providers are skipped');
equal(rateText(null),'—','empty rate shows dash');
equal(secondsText(null),'—','empty seconds shows dash');
equal(rateText(96.55),'96.6%','rate formatting');
equal(secondsText(0.285),'0.285 s','seconds formatting');
`
	command := exec.Command(node, "--check", "-")
	command.Stdin = strings.NewReader(script)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("dashboard card cache-rate script syntax: %v\n%s", err, output)
	}
	command = exec.Command(node, "-")
	command.Stdin = strings.NewReader(script)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("dashboard card cache-rate execution: %v\n%s", err, output)
	}
}

func TestDashboardSourceFilterUsesSharedQueryScope(t *testing.T) {
	html := dashboardHTML
	for _, required := range []string{
		`function initializeSourceFilter()`,
		`select.id='sourceFilter'`,
		`rangeButton.insertAdjacentElement('afterend',select)`,
		`function renderSourceOptions(sources)`,
		`currentData&&currentData.sources`,
		`params.set('source',selectedSource)`,
		`load(true).catch(function(error){text('error',error.message);})`,
	} {
		if !strings.Contains(html, required) {
			t.Fatalf("dashboard missing source-filter contract %q", required)
		}
	}
}

func TestDashboardPreservesReverseProxyPathPrefix(t *testing.T) {
	html := dashboardHTML
	for _, required := range []string{
		`function readDashboardRoute()`,
		`marker='/v0/resource/plugins/'`,
		`index=path.lastIndexOf(marker)`,
		`publicPathPrefix:path.slice(0,index)`,
		`var publicPathPrefix=dashboardRoute.publicPathPrefix`,
		`var resourceBase=publicPathPrefix+'/v0/resource/plugins/'`,
		`var managementBase=publicPathPrefix+'/v0/management/plugins/'`,
		`var modelsURL=publicPathPrefix+'/v1/models'`,
	} {
		if !strings.Contains(html, required) {
			t.Fatalf("dashboard missing reverse-proxy path-prefix contract %q", required)
		}
	}
	for _, forbidden := range []string{
		`var resourceBase='/v0/resource/plugins/'`,
		`var managementBase='/v0/management/plugins/'`,
		`var modelsURL='/v1/models'`,
		`parts.indexOf('plugins')`,
	} {
		if strings.Contains(html, forbidden) {
			t.Fatalf("dashboard retains root-only path construction %q", forbidden)
		}
	}
}

func TestDashboardUsesExactBackendCostsAndPricingSync(t *testing.T) {
	html := fullDashboardHTML
	for _, required := range []string{
		`var costsURL=resourceBase+'/costs'`,
		`var pricesURL=resourceBase+'/full-mode/prices'`,
		`var savePricesURL=resourceBase+'/full-mode/prices/save'`,
		`var syncPricesURL=resourceBase+'/full-mode/prices/sync'`,
		`id="pricingDialog"`,
		`id="cliModelsKeyInput"`,
		`id="loadCLIModels"`,
		`id="priceEditModel"`,
		`id="addPriceEntry"`,
		`id="priceEditDialog"`,
		`priceSearchQuery=''`,
		`function openPriceEditDialog(name)`,
		`function renderPriceTable()`,
		`function collectEditEntry(name)`,
		`if(base.updated_at)entry.updated_at=base.updated_at`,
		`priceEditCurrent=''`,
		`var modelsURL=publicPathPrefix+'/v1/models'`,
		`function normalizeCLIModels(payload)`,
		`async function fetchCLIModels(renderEditor)`,
		`cliModelsPromise=api(modelsURL`,
		`api(costsURL+'?'+query)`,
		`currentCosts.models`,
		`price_book_revision`,
		`priced_requests`,
		`unpriced_requests`,
		`input_usd`,
		`output_usd`,
		`cache_read_usd`,
		`cache_creation_usd`,
		`total_usd`,
		`estimated_cost`,
		`accounting_mode`,
		`tier_threshold`,
		`context_tiers`,
		`service_tiers`,
		`provider_priority`,
		`ignored_suffixes`,
		`mappings`,
		`last_sync`,
		`source:'models.dev'`,
		`fullModePayloadRequest(savePricesURL,{prices:next,sync_settings:settings})`,
		`fullModePayloadRequest(syncPricesURL,{source:'models.dev',models:models,sync_settings:settings},25000)`,
		`value*Number(exchangeRate.rate||0)`,
		`formatTokenTotal(summary.total_tokens)`,
		`renderVisuals();await loadRequests();return responses`,
		`function finishPricingDialogClose(token)`,
		`function closePricingDialog(restoreFocus)`,
		`function openPricing()`,
		`pricingDialogAnimationToken`,
		`pricingDialogCloseTimer`,
		`pricingDialog.classList.add('is-open')`,
		`pricingDialog.classList.add('is-closing')`,
		`pricingDialog.addEventListener('transitionend'`,
		`pricingDialog.addEventListener('cancel',function(event){event.preventDefault();closePricingDialog(true);})`,
		`dialog#pricingDialog.is-open{opacity:1`,
		`dialog#pricingDialog.is-closing{transition-duration:120ms}`,
		`dialog#pricingDialog::backdrop{background:rgb(0 0 0/0)`,
		`if(priceEditDialog.open)priceEditDialog.close();`,
		`pricingDialog.addEventListener('close',function(){clearCLIModelState();if(priceEditDialog.open)priceEditDialog.close();})`,
		`tier-edit-fields`,
		`id="priceEditAddTier"`,
		`remove-context-tier`,
		`remove-model-price`,
		`function syncEditTierFields()`,
		`priceEditCurrent=''`,
		`t('pricing.editTitle')`,
		`function restorePriceEntry(name)`,
		`restore-cell`,
		`t('pricing.restoreConfirm'`,
		`function savePriceEntry(name,entry)`,
		`savePriceEntry(name,null)`,
		`delete next[name]`,
		`clearCLIModelState()`,
		`id="providerPriority"`,
		`id="ignoredSuffixes"`,
		`id="syncMappings"`,
		`id="syncPrices"`,
		`t('pricing.coverageWaiting')`,
		`t('pricing.tagUnpriced')`,
		`t('pricing.syncingCatalog')`,
		`t('pricing.syncFailed'`,
	} {
		if !strings.Contains(html, required) {
			t.Fatalf("dashboard missing exact-cost/pricing contract %q", required)
		}
	}
	for _, forbidden := range []string{
		`costFor(name,input,output)`,
		`costFor(`,
		`localStorage`,
		`sessionStorage`,
		`fetch('https://models.dev`,
		`fetch("https://models.dev`,
		`fetch('https://open.er-api.com`,
		`fetch("https://open.er-api.com`,
	} {
		if strings.Contains(html, forbidden) {
			t.Fatalf("dashboard contains forbidden pricing pattern %q", forbidden)
		}
	}
}

func TestDashboardUsesSingleMonthLocalDateRangePicker(t *testing.T) {
	html := dashboardHTML
	for _, required := range []string{
		`id="rangeButton"`,
		`aria-expanded="false" aria-controls="dateRangePopover"`,
		`id="dateRangePopover" class="date-range-popover" role="dialog" aria-modal="false"`,
		`id="dateRangeTitle"`,
		`id="calendarPanels" class="calendar-panels"`,
		`id="calendar" class="calendar-panel"`,
		`id="confirmDateRange"`,
		`id="resetDateRange"`,
		`id="startTime" class="native-time-source" type="time" step="1"`,
		`id="endTime" class="native-time-source" type="time" step="1"`,
		`id="startTimeButton" class="time-picker-button" type="button" aria-haspopup="dialog"`,
		`id="endTimeButton" class="time-picker-button" type="button" aria-haspopup="dialog"`,
		`id="startTimePicker" class="time-picker-surface" role="dialog"`,
		`id="endTimePicker" class="time-picker-surface" role="dialog"`,
		`data-time-part="hour" type="number" max="23"`,
		`data-time-part="minute" type="number" max="59"`,
		`data-time-part="second" type="number" max="59"`,
		`limit=index===0?23:59,value=Math.min(limit,Math.floor(Number(field.value)));if(value<0)value=limit;`,
		`function initializeTimePickers()`,
		`function closeTimePickers(restoreFocus)`,
		`function finishTimePickerClose(picker,token)`,
		`input.dispatchEvent(new Event('input',{bubbles:true}))`,
		`.time-picker-surface.is-open{opacity:1`,
		`.time-picker-surface.is-closing{transition-duration:120ms}`,
		`event.key==='Escape'&&activeTimePicker`,
		`data-i18n="range.endExclusive"`,
		`function setDraftTime(boundary,value)`,
		`draftRangeStart.getTime()<draftRangeEnd.getTime()`,
		`document.getElementById('startTime').addEventListener('input'`,
		`document.getElementById('endTime').addEventListener('input'`,
		`appliedRangeStart.toISOString()`,
		`appliedRangeEnd.toISOString()`,
		`function parseSavedRange(value)`,
		`select.value=duration<6*60*60*1000?'minute':'hour';`,
		`function positionDateRangePopover()`,
		`placeBelow=below>=popoverHeight||below>=above`,
		`dateRangePopover.dataset.placement=placeBelow?'bottom':'top'`,
		`function closeDateRange(restoreFocus)`,
		`function finishDateRangeClose(token)`,
		`token!==dateRangeAnimationToken`,
		`dateRangePopover.classList.add('is-closing')`,
		`dateRangePopover.classList.add('is-opening')`,
		`dateRangePopover.classList.add('is-open')`,
		`var wasHidden=dateRangePopover.hidden`,
		`clearTimeout(dateRangeCloseTimer)`,
		`dateRangeCloseTimer=setTimeout(function(){finishDateRangeClose(token);},180)`,
		`event.propertyName==='opacity'`,
		`dateRangePopover.hidden=true`,
		`dateRangePopover.hidden=false`,
		`rangeButton.setAttribute('aria-expanded','true')`,
		`dateRangePopover.hidden||dateRangePopover.classList.contains('is-closing')`,
		`event.key==='Escape'&&!dateRangePopover.hidden`,
		`event.composedPath?event.composedPath():[]`,
		`path.indexOf(rangeButton)<0&&path.indexOf(dateRangePopover)<0`,
		`dateRangePopover.addEventListener('click',function(event){event.stopPropagation()`,
		`id="quickRanges" class="quick-ranges" role="group"`,
		`data-range-preset="last_5_hours"`,
		`data-range-preset="last_7_days"`,
		`data-range-preset="last_30_days"`,
		`data-range-preset="current_month"`,
		`button.setAttribute('aria-pressed'`,
		`function applyRangePreset(mode)`,
		`function dateRangeQuery()`,
		`function resolvedDateRange()`,
		`now.getTime()-5*60*60*1000`,
		`today.getFullYear(),today.getMonth(),today.getDate()-6`,
		`today.getFullYear(),today.getMonth(),today.getDate()-29`,
		`today.getFullYear(),today.getMonth(),today.getDate()+1`,
		`new Date(today.getFullYear(),today.getMonth(),1)`,
		`draftRangeMode='custom'`,
		`document.getElementById('confirmDateRange').disabled=!complete`,
		`params.set('start',range.start.toISOString())`,
		`params.set('end',range.end.toISOString())`,
		`scheduleDashboardPreferencesSave();closeDateRange(true)`,
		`panels.classList.add(delta>0?'is-shifting-next':'is-shifting-previous')`,
		`.calendar-panels.is-shifting-next .calendar-panel{animation:calendar-enter-next`,
		`.calendar-panels.is-shifting-previous .calendar-panel{animation:calendar-enter-previous`,
		`if(reducedMotion)return`,
		`.calendar-panels{display:grid;grid-template-columns:minmax(0,1fr)`,
		`.date-range-popover{--date-popover-shift:-6px;position:fixed`,
		`transform:translateY(var(--date-popover-shift)) scale(.98)`,
		`.date-range-popover[data-placement='top']`,
		`.date-range-popover.is-open{opacity:1;pointer-events:auto`,
		`.date-range-popover.is-closing{transition-duration:120ms}`,
		`@media(prefers-reduced-motion:reduce)`,
		`@media(max-width:560px){.range-control`,
		`width:min(420px,calc(100vw - 28px))`,
		`function renderCalendarPanel(container,month)`,
		`prevButton.dataset.calendarShift='-1'`,
		`nextButton.dataset.calendarShift='1'`,
		`rangeSelectionStage='range'`,
		`draftRangeEnd=new Date(selectedDate.getFullYear(),selectedDate.getMonth(),selectedDate.getDate()+1);rangeSelectionStage='day';`,
		`' · '+t('range.extendHint')`,
		`calendarBaseMonth=new Date(draftRangeStart.getFullYear(),draftRangeStart.getMonth(),1)`,
	} {
		if !strings.Contains(html, required) {
			t.Fatalf("dashboard missing single-month date range picker contract %q", required)
		}
	}
	for _, forbidden := range []string{
		`<select id="range"`,
		`document.getElementById('range').addEventListener('change'`,
		`<dialog id="dateRangeDialog"`,
		`dateRangeDialog.showModal()`,
		`id="calendarRight"`,
		`calendarLeft`,
		`calendarBaseMonth.getFullYear(),calendarBaseMonth.getMonth()+1`,
		`grid-template-columns:repeat(2,minmax(0,1fr));gap:18px`,
		`width:min(760px,calc(100vw - 28px))`,
		`data-time-part="hour" type="number" min=`,
		`data-time-part="minute" type="number" min=`,
		`data-time-part="second" type="number" min=`,
		`value=Math.max(0,Math.min(limit,Math.floor(Number(field.value))))`,
	} {
		if strings.Contains(html, forbidden) {
			t.Fatalf("dashboard retains obsolete two-month date range picker pattern %q", forbidden)
		}
	}
}

func TestDashboardPaintsDarkBeforeRunningThemeSync(t *testing.T) {
	html := dashboardHTML
	rootStart := strings.Index(html, `<html lang="zh-CN" data-theme="dark" style="background:#151412;color-scheme:dark">`)
	initialStyle := strings.Index(html, `<style id="initial-theme">`)
	initialScript := strings.Index(html, `<script>`)
	if rootStart < 0 || initialStyle < 0 || initialScript < 0 || rootStart > initialStyle || initialStyle > initialScript {
		t.Fatal("dark root background and initial stylesheet must be available before theme sync script runs")
	}
}

func TestDashboardSynchronizesHostFrameBackground(t *testing.T) {
	html := dashboardHTML
	for _, required := range []string{
		"getComputedStyle(parentRoot).getPropertyValue('--bg-secondary')",
		"root.style.backgroundColor=background",
		"window.frameElement.style.backgroundColor=background",
		"window.frameElement.parentElement.style.backgroundColor=background",
	} {
		if !strings.Contains(html, required) {
			t.Fatalf("dashboard missing host background sync %q", required)
		}
	}
}

func TestDashboardResponseHeaders(t *testing.T) {
	response := dashboardResponse()
	if response.Headers.Get("Cache-Control") != "no-store" {
		t.Fatal("missing no-store")
	}
	if response.Headers.Get("Referrer-Policy") != "no-referrer" {
		t.Fatal("missing referrer policy")
	}
	csp := response.Headers.Get("Content-Security-Policy")
	for _, directive := range []string{"default-src 'none'", "connect-src 'self'", "base-uri 'none'", "form-action 'none'"} {
		if !strings.Contains(csp, directive) {
			t.Fatalf("CSP missing %q: %s", directive, csp)
		}
	}
}

func TestFullModeUsesSeparateProtectedDashboard(t *testing.T) {
	if !strings.Contains(dashboardHTML, `id="fullModeButton"`) || !strings.Contains(dashboardHTML, `id="fullModeDialog"`) {
		t.Fatal("dashboard must provide a full-mode entry button and dialog")
	}
	if !strings.Contains(dashboardHTML, `managementBase+'/full-mode/session'`) || !strings.Contains(dashboardHTML, `method:'POST'`) || !strings.Contains(dashboardHTML, `Authorization':'Bearer '+key`) {
		t.Fatal("full-mode dialog must create an authenticated full-mode session")
	}
	if !strings.Contains(dashboardHTML, `resourceBase+'/full-dashboard#session='+encodeURIComponent(session)`) || strings.Contains(dashboardHTML, `fullModeManagementKey=key`) {
		t.Fatal("homepage must navigate with the opaque session token without retaining the management key")
	}
	for _, forbidden := range []string{
		`id="pricingButton"`, `id="pricingDialog"`, `id="priceList"`, `id="savePricing"`, `id="syncPrices"`,
		`id="exportButton"`, `id="exportMenu"`, `id="exportCSV"`, `id="exportPNG"`, `id="exportBackup"`, `id="restoreBackup"`, `id="backupDialog"`,
		`function exportCSV()`, `function exportPNG()`, `function downloadBackup()`, `function restoreBackup()`, `function confirmAndRestore(file)`,
		`document.getElementById('exportBackup').addEventListener`, `document.getElementById('restoreBackup').addEventListener`,
	} {
		if strings.Contains(dashboardHTML, forbidden) {
			t.Fatalf("normal dashboard must not expose pricing UI %q", forbidden)
		}
	}
	if !strings.Contains(dashboardHTML, `function initializePricingSelectEnhancement(){var list=document.getElementById('priceList');if(!list)return;`) {
		t.Fatal("normal dashboard must skip pricing select initialization when full-mode pricing UI is absent")
	}
	if !strings.Contains(dashboardHTML, `function openDateRange(){closeActiveDropdown(false);if(typeof closeExportMenu==='function')closeExportMenu();`) {
		t.Fatal("normal dashboard date range picker must not require the removed export menu script")
	}
	for _, required := range []string{`var fullModePage=true`, `button.exitFullMode`, `history.replaceState(null,'',window.location.pathname+window.location.search)`} {
		if !strings.Contains(fullDashboardHTML, required) {
			t.Fatalf("full dashboard missing %q", required)
		}
	}
	if !strings.Contains(fullDashboardHTML, `X-Full-Mode-Session`) || !strings.Contains(fullDashboardHTML, `resourceBase+'/full-mode/prices'`) || !strings.Contains(fullDashboardHTML, `function openPricing(){if(!fullModeEnabled||!fullModeSession)return;`) || strings.Contains(fullDashboardHTML, `fullModeManagementKey=key`) {
		t.Fatal("full dashboard must use the server-issued capability for protected endpoints")
	}
	for _, required := range []string{
		`id="pricingButton" class="control"`, `id="pricingDialog"`, `id="priceList"`, `id="saveSyncSettings"`, `id="syncPrices"`, `id="priceEditDialog"`, `id="addPriceEntry"`,
		`id="exportButton"`, `id="exportMenu"`, `id="exportCSV"`, `id="exportPNG"`, `id="exportBackup"`, `id="restoreBackup"`, `id="backupDialog"`,
		`resourceBase+'/full-mode/backup'`, `resourceBase+'/full-mode/restore'`, `async function requireFullModeExportSession()`, `await fullModeBinaryPayloadRequest(restoreURL,file,120000)`,
	} {
		if !strings.Contains(fullDashboardHTML, required) {
			t.Fatalf("full dashboard must provide pricing UI %q", required)
		}
	}
	if !strings.Contains(fullDashboardHTML, `var resetURL=resourceBase+'/full-mode/reset';`) || !strings.Contains(fullDashboardHTML, `async function resetStats(){if(!fullModeEnabled||!fullModeSession){text('error',t('fullMode.keyRequired'));return;}`) {
		t.Fatal("full dashboard reset must use the session-protected resource route")
	}
	for _, forbidden := range []string{`askBackupManagementKey`, `managementBase+'/backup'`, `managementBase+'/restore'`, `Authorization':'Bearer '+managementKey,'Content-Type':'application/octet-stream'`, `function askManagementKey()`, `id="resetDialog"`, `resetKeyInput`, `Authorization':'Bearer '+managementKey`} {
		if strings.Contains(fullDashboardHTML, forbidden) {
			t.Fatalf("full dashboard export must not use a management key %q", forbidden)
		}
	}
	if strings.Contains(fullDashboardHTML, `sensitive_data":[]`) {
		t.Fatal("full dashboard HTML must not embed protected data")
	}
}

func TestDashboardDoesNotServerRenderUsageValues(t *testing.T) {
	malicious := `</td><script>alert(1)</script>`
	if strings.Contains(dashboardHTML, malicious) {
		t.Fatal("dashboard unexpectedly embeds usage fixture")
	}
	if !strings.Contains(dashboardHTML, "td.textContent=value") {
		t.Fatal("usage cells are not rendered with textContent")
	}
}

func TestDashboardHeaderKeepsHostClearanceAndControlGroups(t *testing.T) {
	html := dashboardHTML
	for _, required := range []string{
		`.heading{min-width:0;padding:2px clamp(96px,10vw,152px) 0 0}`,
		`class="control-group control-filters"`,
		`class="control-group control-actions"`,
		`flex-wrap:nowrap`,
		`.control-actions{flex:0 0 auto;margin-left:auto}`,
		`button.control{display:inline-flex;align-items:center;justify-content:center;gap:7px;padding:8px 12px;font-weight:650;white-space:nowrap}`,
		`@media(max-width:820px){.heading{padding-right:clamp(72px,12vw,112px)}`,
		`.control-filters{flex-basis:100%}`,
		`.control-actions{margin-left:0}`,
	} {
		if !strings.Contains(html, required) {
			t.Fatalf("dashboard missing header layout contract %q", required)
		}
	}
	for _, forbidden := range []string{
		`--host-overlay-safe-inset`,
		`<label class="language-control">`,
		`id="languageSelect"`,
		`__MSG_`,
	} {
		if strings.Contains(html, forbidden) {
			t.Fatalf("dashboard unexpectedly contains forbidden header markup %q", forbidden)
		}
	}
}

func TestDashboardAnalysisSeriesTogglesAreAccessible(t *testing.T) {
	html := dashboardHTML
	for _, required := range []string{
		`class="series-key" role="group"`,
		`data-i18n-aria="analysis.series.aria"`,
		`class="series-key-button" data-series="ttft" aria-pressed="false"`,
		`class="series-key-button" data-series="latency" aria-pressed="false"`,
		`class="series-key-button" data-series="requests" aria-pressed="true"`,
		`class="series-key-button" data-series="successRate" aria-pressed="true"`,
		`class="series-key-button" data-series="cacheRate" aria-pressed="true"`,
		`data-series="totalTokens"`,
		`data-series="input"`,
		`data-series="output"`,
		`data-series="cacheRead"`,
		`function toggleTrendSeries(key)`,
		`function trendSeriesVisible(key)`,
		`hiddenTrendSeries=new Set(['ttft','latency','totalTokens','input','output','cacheRead'])`,
		`button.setAttribute('aria-pressed',visible?'true':'false')`,
		`t('analysis.series.hide',{series:trendSeriesLabel(key)})`,
		`t('analysis.series.show',{series:trendSeriesLabel(key)})`,
		`t('analysis.series.allHidden')`,
		`syncTrendSeriesButtons();renderAnalysis();`,
		`.series-key-button[aria-pressed='false']`,
	} {
		if !strings.Contains(html, required) {
			t.Fatalf("dashboard missing analysis series toggle contract %q", required)
		}
	}
}

func TestDashboardAnalysisZoomHelpDoesNotOverlapAxis(t *testing.T) {
	html := dashboardHTML
	for _, required := range []string{
		`.chart-wrap{position:relative;display:grid;grid-template-rows:minmax(0,1fr) auto auto`,
		`.chart-footer{display:flex;justify-content:flex-end;align-items:center;min-height:22px`,
		`class="chart-footer" aria-hidden="true"`,
		`.zoom-tip{color:var(--text-quaternary);font-size:10px;line-height:1.3;pointer-events:none`,
		`.chart-wrap{min-height:300px}.chart-wrap svg{min-height:270px;height:100%}`,
		`.chart-footer{display:none}`,
		`document.getElementById('analysisWrap').addEventListener('wheel'`,
		`data-i18n="trend.zoomTip"`,
	} {
		if !strings.Contains(html, required) {
			t.Fatalf("dashboard missing zoom-help geometry contract %q", required)
		}
	}
	if !strings.Contains(html, `<div class="chart-footer" aria-hidden="true"><div class="zoom-tip" data-i18n="trend.zoomTip">`) {
		t.Fatal("zoom tip must be placed in chart-footer below the svg")
	}
	for _, forbidden := range []string{
		`.zoom-tip{position:absolute;right:6px;bottom:2px`,
		`.chart-wrap,.chart-wrap svg{min-height:300px;height:300px}`,
	} {
		if strings.Contains(html, forbidden) {
			t.Fatalf("dashboard retained overlapping zoom-help layout %q", forbidden)
		}
	}
	if strings.Contains(html, `</svg><div class="zoom-tip"`) {
		t.Fatal("zoom tip still overlays the svg plot area")
	}
}

func TestDashboardChartAccessibilityIsKeyboardOperable(t *testing.T) {
	html := dashboardHTML
	for _, required := range []string{
		"class:'bar-hit',tabindex:0",
		"'aria-label':point.label+' '+parts.join(' ')",
		"hit.dataset.index=index;",
		"function initializeAnalysisTooltipDelegation(){var chart=document.getElementById('analysisChart');if(!chart)return;var active=null;",
		"chart.addEventListener('mouseover',function(event){var point=pointAt(event.target);if(point&&active!==point){active=point;showAnalysisTooltip(event,point);}});",
		"chart.addEventListener('mousemove',function(event){var point=pointAt(event.target);if(point)moveTooltip(event);else if(active){active=null;hideTooltip();}});",
		"chart.addEventListener('mouseleave',function(){if(active){active=null;hideTooltip();}});",
		"chart.addEventListener('focusin',function(event){var point=pointAt(event.target);if(point){active=point;showAnalysisTooltip(event,point);}});",
		"chart.addEventListener('focusout',function(){if(active){active=null;hideTooltip();}});",
		"if(event.key==='Enter'||event.key===' ')",
		"event.preventDefault();selectModel(modelName(group.model));",
		"td.classList.add('model-cell')",
		"td.setAttribute('role','button')",
		`data-i18n-aria="trend.zoomOut.title"`,
		`data-i18n-aria="trend.zoomIn.title"`,
		`.bar-hit:focus-visible`,
		`class="series-key-button"`,
		`aria-pressed="true"`,
	} {
		if !strings.Contains(html, required) {
			t.Fatalf("dashboard missing interactive chart accessibility behavior %q", required)
		}
	}
}

func TestDashboardSummaryCardsShowAllTwelveMetrics(t *testing.T) {
	html := dashboardHTML
	for _, required := range []string{
		`animateText('avgTTFT',secondsText(avgTTFT))`,
		`animateText('avgLatency',secondsText(avgLatency))`,
		`animateText('totalCalls',fmt(totalCalls))`,
		`animateText('successCalls',fmt(successCalls))`,
		`animateText('successRate',rateText(successRate))`,
		`animateText('failedCalls',fmt(failedCalls))`,
		`animateText('failureRate',rateText(failureRate))`,
		`animateText('totalTokens',formatTokenTotal(summary.total_tokens))`,
		`animateText('inputTokens',formatTokenTotal(summary.input_tokens))`,
		`animateText('outputTokens',formatTokenTotal(summary.output_tokens))`,
		`animateText('cacheReadTokens',formatTokenTotal(summary.cache_read_tokens))`,
		`animateText('cacheRate',rateText(cacheRate))`,
		`successCalls=Math.max(0,totalCalls-failedCalls)`,
		`successRate=totalCalls?successCalls/totalCalls*100:null`,
		`failureRate=totalCalls?failedCalls/totalCalls*100:null`,
		`kindSums.read/kindSums.denominator*100`,
		`avgTTFT=Number(summary.ttft_samples||0)>0?Number(summary.total_ttft_ns||0)/Number(summary.ttft_samples||0)/1e9:null`,
		`avgLatency=Number(summary.latency_samples||0)>0?Number(summary.total_latency_ns||0)/Number(summary.latency_samples||0)/1e9:null`,
		`cache_read_tokens`,
	} {
		if !strings.Contains(html, required) {
			t.Fatalf("dashboard missing summary card metric %q", required)
		}
	}
	for _, forbidden := range []string{
		`animateText('totalCost'`,
		`animateText('topModel'`,
		`animateText('requests'`,
		`text('requestDetail'`,
		`text('tokenDetail'`,
		`summary.total_tokens=summary.input_tokens`,
	} {
		if strings.Contains(html, forbidden) {
			t.Fatalf("dashboard retained obsolete summary card behavior via %q", forbidden)
		}
	}
}

func TestDashboardSummaryCardsShareUniformVerticalRhythm(t *testing.T) {
	html := dashboardHTML
	for _, required := range []string{
		`.card{position:relative;display:grid;grid-template-rows:auto minmax(2.4em,1fr) auto`,
		`.card .label{display:flex;align-items:center;gap:7px;min-height:28px`,
		`.card .value{position:relative;z-index:1;display:flex;align-items:center;margin-top:10px;min-height:2.4em`,
		`.card .detail{position:relative;z-index:1;margin-top:8px;min-height:2.7em`,
		`line-height:1.35`,
		`.card-switch{position:relative;z-index:2;margin-left:auto;min-height:28px`,
		`id="tokenUnitButton" class="card-switch"`,
		`.card::after{content:"";position:absolute;right:-29px;bottom:-36px;width:90px;height:90px`,
		`.cards{display:grid;grid-template-columns:repeat(6,minmax(118px,1fr))`,
		`@media(max-width:820px){.heading{padding-right:clamp(72px,12vw,112px)}`,
	} {
		if !strings.Contains(html, required) {
			t.Fatalf("dashboard missing summary-card rhythm contract %q", required)
		}
	}
	for _, forbidden := range []string{
		`id="modelBadge"`,
		`id="currencyButton"`,
		`class="value model-value"`,
	} {
		if strings.Contains(html, forbidden) {
			t.Fatalf("dashboard retained obsolete summary card markup %q", forbidden)
		}
	}
}

func TestPricingDialogSyncHelpAndScrollContract(t *testing.T) {
	// The pricing dialog must keep wheel scrolling inside itself: once the
	// dialog reaches its scroll end it must not chain to the page behind,
	// and the page behind must not scroll while any modal dialog is open.
	// The load-models hint sits vertically centered against its button, and
	// each sync-settings textarea carries a public-facing help line.
	required := []string{
		`box-shadow:0 24px 70px rgb(0 0 0/.32);overscroll-behavior:contain}`,
		`html:has(dialog:modal){overflow:hidden}`,
		`<div class="model-source-main"><button id="loadCLIModels" class="control" type="button" data-i18n="button.loadModels">Load current models</button><div class="model-source-status" data-i18n="pricing.modelSourceHint">`,
		`</div></div></div><div class="table-scroll price-table-scroll">`,
		`.model-source-main{display:flex;align-items:center;gap:10px;flex-wrap:wrap}`,
		`id="providerPriority" autocomplete="off"></textarea><small data-i18n="pricing.providerPriorityHelp">`,
		`id="ignoredSuffixes" autocomplete="off"></textarea><small data-i18n="pricing.ignoredSuffixesHelp">`,
		`id="syncMappings" autocomplete="off"></textarea><small data-i18n="pricing.mappingsHelp">`,
		`.sync-field small{color:var(--text-tertiary);font-size:10px;font-weight:500;line-height:1.5}`,
	}
	html := fullDashboardHTML
	for _, item := range required {
		if !strings.Contains(html, item) {
			t.Fatalf("pricing dialog missing sync help/scroll contract %q", item)
		}
	}
}

func TestDashboardLocalesCatalog(t *testing.T) {
	// All four locale codes must be embedded in the HTML.
	for _, code := range []string{"en", "zh-CN", "zh-TW", "ru"} {
		if !strings.Contains(dashboardHTML, `"`+code+`"`) {
			t.Fatalf("dashboardHTML missing locale code %q", code)
		}
	}

	// The embedded JSON blob must decode to a map containing all four locales
	// with the required base keys.
	const marker = "/*LOCALE_PLACEHOLDER*/"
	if strings.Contains(dashboardHTML, marker) {
		t.Fatal("dashboardHTML still contains unresolved locale placeholder")
	}

	// Verify each locale file individually via the embed FS.
	requiredKeys := []string{
		"app.title",
		"button.refresh",
		"button.reset",
		"button.downloadBackup",
		"button.restoreBackup",
		"backup.preparing",
		"backup.success",
		"backup.failed",
		"backup.keyPrompt",
		"backup.restoreWarning",
		"backup.restoring",
		"backup.restored",
		"backup.fileTooLarge",
		"status.loading",
		"status.costRefreshing",
		"status.costRefreshFailed",
		"chart.noCalls",
		"chart.noRequests",
		"table.cacheHitRate",
		"sourceFilter.label",
		"sourceFilter.all",
		"range.quickRanges",
		"range.lastFiveHours",
		"range.lastSevenDays",
		"range.lastThirtyDays",
		"range.currentMonth",
		"range.startTime",
		"range.endTime",
		"range.endExclusive",
		"range.invalidTimeRange",
		"card.successRate",
		"card.ttft",
		"card.latency",
		"card.totalCalls",
		"card.success",
		"card.failed",
		"card.failureRate",
		"card.cacheRate",
		"card.totalTokens",
		"card.input",
		"card.output",
		"card.cacheRead",
		"requestFilter.model",
		"requestFilter.source",
		"requestFilter.result",
		"requestFilter.allModels",
		"requestFilter.allSources",
		"requestFilter.allResults",
		"empty.calls",
		"requestColumns.button",
		"requestColumns.title",
		"requestColumns.showAll",
		"requestColumns.hide",
		"pagination.rowsPerPage",
		"sort.ascending",
		"sort.descending",
		"model.untitled",
		"pricing.title",
		"button.addPricing",
		"button.saveSyncSettings",
		"pricing.providerPriorityHelp",
		"pricing.ignoredSuffixesHelp",
		"pricing.mappingsHelp",
		"pricing.colInput",
		"pricing.colOutput",
		"pricing.colCacheRead",
		"pricing.colCacheWrite",
		"pricing.tagReference",
		"pricing.tagCustom",
		"pricing.tagUnpriced",
		"pricing.edit",
		"pricing.remove",
		"pricing.referenceLockHint",
		"pricing.confirmDelete",
		"pricing.editTitle",
		"pricing.addTitle",
		"pricing.modelId",
		"pricing.fieldInput",
		"pricing.fieldOutput",
		"pricing.fieldCacheRead",
		"pricing.fieldCacheWrite",
		"pricing.tieredSwitch",
		"pricing.save",
		"pricing.savedEntry",
		"pricing.deletedEntry",
		"pricing.duplicateModel",
		"pricing.invalidMapping",
		"pricing.tierColThreshold",
		"pricing.tierColInput",
		"pricing.tierColOutput",
		"pricing.tierColCacheRead",
		"pricing.tierColCacheWrite",
		"pricing.restoreCol",
		"pricing.restore",
		"pricing.restoredEntry",
		"pricing.restoreConfirm",
		"pricing.restoreReferenceHint",
		"pricing.addTier",
		"error.missingModelId",
		"analysis.title",
		"analysis.subtitle",
		"analysis.series.aria",
		"analysis.series.hide",
		"analysis.series.show",
		"analysis.series.allHidden",
		"analysis.chart.aria",
		"analysis.selected",
		"analysis.modelDrill",
		"result.success",
		"result.failed",
		"result.failedHttp",
	}
	for _, code := range []string{"en", "zh-CN", "zh-TW", "ru"} {
		data, err := localeFS.ReadFile("locales/" + code + ".json")
		if err != nil {
			t.Fatalf("locale %s: %v", code, err)
		}
		var m map[string]string
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("locale %s invalid JSON: %v", code, err)
		}
		for _, key := range requiredKeys {
			if _, ok := m[key]; !ok {
				t.Fatalf("locale %s missing required key %q", code, key)
			}
		}
	}

	// Guard translateRawResult and its call sites so Simplified-Chinese
	// backend result literals stay mapped through the locale catalog.
	for _, required := range []string{
		"function translateRawResult(raw,failed)",
		`/^失败`,
		"translateRawResult(item.result,item.failed)",
		"case 'result':return translateRawResult(item.result,item.failed);",
	} {
		if !strings.Contains(dashboardHTML, required) {
			t.Fatalf("dashboardHTML missing translateRawResult coverage %q", required)
		}
	}
	if !strings.Contains(fullDashboardHTML, "translateRawResult(record.result,record.failed)") {
		t.Fatal("full dashboardHTML missing export translateRawResult coverage")
	}

	// Verify the locale runtime is wired: translateStatic must be called
	// during initialisation (after theme and chart resize setup).
	initSeq := "initializeThemeSync();initializeChartResize();translateStatic();"
	if !strings.Contains(dashboardHTML, initSeq) {
		t.Fatalf("dashboardHTML missing init sequence %q", initSeq)
	}
}

func TestDashboardLocalizesUntitledModelInAllLocales(t *testing.T) {
	for _, code := range []string{"en", "zh-CN", "zh-TW", "ru"} {
		data, err := localeFS.ReadFile("locales/" + code + ".json")
		if err != nil {
			t.Fatalf("read locale %s: %v", code, err)
		}
		var values map[string]string
		if err := json.Unmarshal(data, &values); err != nil || strings.TrimSpace(values["model.untitled"]) == "" {
			t.Fatalf("locale %s has invalid model.untitled: %v", code, err)
		}
	}
	if !strings.Contains(dashboardHTML, "function modelName(value){return value&&String(value).trim()?String(value):t('model.untitled');}") {
		t.Fatal("dashboard does not localize backend-neutral unnamed models")
	}
}

func TestDashboardTemplateMarkersAreUniqueAndReplaced(t *testing.T) {
	markers := []string{
		"/*LOCALE_PLACEHOLDER*/",
		"/*FULL_MODE_PAGE*/",
		"/*FULL_MODE_APIKEY_STYLES*/",
		"/*FULL_MODE_APIKEY_FILTER*/",
		"/*FULL_MODE_APIKEY_MARKUP*/",
		"/*FULL_MODE_APIKEY_DIALOG*/",
		"/*FULL_MODE_APIKEY_QUERY*/",
		"/*FULL_MODE_APIKEY_LOAD*/",
		"/*FULL_MODE_APIKEY_RENDER*/",
		"/*FULL_MODE_APIKEY_DIMENSION_COLUMN*/",
		"/*FULL_MODE_APIKEY_DIMENSION_SORT*/",
		"/*FULL_MODE_APIKEY_DIMENSION_CELL*/",
		"/*FULL_MODE_APIKEY_REQUEST_COLUMN*/",
		"/*FULL_MODE_APIKEY_REQUEST_SORT*/",
		"/*FULL_MODE_APIKEY_REQUEST_CELL*/",
		"/*FULL_MODE_APIKEY_LANGUAGE*/",
		"/*FULL_MODE_APIKEY_SCRIPT*/",
	}
	for _, marker := range markers {
		if count := strings.Count(dashboardHTMLTemplate, marker); count != 1 {
			t.Fatalf("template marker %s appears %d times, want 1", marker, count)
		}
		if strings.Contains(dashboardHTML, marker) || strings.Contains(fullDashboardHTML, marker) {
			t.Fatalf("generated dashboard retains marker %s", marker)
		}
	}
}
func TestDashboardScriptParsesWithNode(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not available")
	}
	variants := map[string]string{"dashboard": dashboardHTML, "full": fullDashboardHTML}
	for name, html := range variants {
		start := strings.Index(html, "<script>")
		scriptIndex := 0
		for start >= 0 {
			end := strings.Index(html[start:], "</script>")
			if end < 0 {
				t.Fatalf("%s: unterminated script block", name)
			}
			script := html[start+len("<script>") : start+end]
			resolved := strings.ReplaceAll(script, "/*LOCALE_PLACEHOLDER*/", "{}")
			resolved = strings.ReplaceAll(resolved, "/*FULL_MODE_PAGE*/", "false")
			for _, marker := range []string{"STYLES", "FILTER", "MARKUP", "DIALOG", "QUERY", "LOAD", "RENDER", "DIMENSION_COLUMN", "DIMENSION_SORT", "DIMENSION_CELL", "REQUEST_COLUMN", "REQUEST_SORT", "REQUEST_CELL", "LANGUAGE", "SCRIPT"} {
				resolved = strings.ReplaceAll(resolved, "/*FULL_MODE_APIKEY_"+marker+"*/", "")
			}
			command := exec.Command(node, "--check", "-")
			command.Stdin = strings.NewReader(resolved)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("%s script %d failed node --check: %v\n%s", name, scriptIndex, err, output)
			}
			scriptIndex++
			next := start + end + len("</script>")
			idx := strings.Index(html[next:], "<script>")
			if idx < 0 {
				break
			}
			start = next + idx
		}
	}
}

func TestDashboardAnalysisHasZoomScrollbar(t *testing.T) {
	html := dashboardHTML
	for _, required := range []string{
		`id="analysisScrollbar" class="chart-scrollbar" hidden`,
		`.chart-scrollbar{position:relative;height:10px;flex:none;touch-action:none}`,
		`.chart-scrollbar-thumb{position:absolute;top:1px;bottom:1px;border-radius:999px;background:var(--border-color);cursor:grab}`,
		`.chart-scrollbar-thumb:hover,.chart-scrollbar-thumb.is-dragging{background:var(--border-hover)}`,
		`scrollbar.hidden=!(all.length>1)`,
		`var thumbWidth=Math.max(scrollFraction*100,4);`,
		`scrollThumb.style.left=Math.min(all.length?analysisZoom.start/all.length*100:0,100-thumbWidth)+'%'`,
		`scrollThumb.style.width=thumbWidth+'%'`,
		`drag={x:event.clientX,start:analysisZoom.start,all:lastAnalysis};`,
		`function dragFrameTick(){dragFrame=0;if(!pendingDragRender)return;var now=performance.now();if(now-lastDragRenderAt<30){if(!dragFrame)dragFrame=requestAnimationFrame(dragFrameTick);return;}pendingDragRender=false;lastDragRenderAt=now;renderAnalysis(drag?drag.all:undefined);}`,
		`if(Math.abs(next-analysisZoom.start)<0.0001)return;analysisZoom.start=next;pendingDragRender=true;if(!dragFrame)dragFrame=requestAnimationFrame(dragFrameTick);`,
		`if(dragFrame){cancelAnimationFrame(dragFrame);dragFrame=0;}pendingDragRender=false;drag=null;thumb.classList.remove('is-dragging');renderAnalysis();`,
		`function initializeAnalysisScrollbar()`,
		`initializeAnalysisScrollbar();initializeAnalysisTooltipDelegation();`,
		`thumb.addEventListener('pointerdown',function(event){drag={x:event.clientX,start:analysisZoom.start,all:lastAnalysis};`,
		`scrollbar.addEventListener('pointerdown',function(event){if(event.target===thumb||lastAnalysis.length<2)return;`,
		`.chart-wrap{position:relative;display:grid;grid-template-rows:minmax(0,1fr) auto auto`,
	} {
		if !strings.Contains(html, required) {
			t.Fatalf("dashboard missing analysis zoom scrollbar contract %q", required)
		}
	}
}
