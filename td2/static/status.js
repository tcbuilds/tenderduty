
async function loadState() {
   const enableLogs = await fetch("logsenabled", {
        method: 'GET',
        mode: 'cors',
        cache: 'no-cache',
        credentials: 'same-origin',
        redirect: 'error',
        referrerPolicy: 'no-referrer'
    });
    let showLog
    try {
        showLog = await enableLogs.json()
    } catch(e) {
        console.log(e)
    }
    if (showLog.enabled === false) {
        document.getElementById("logContainer").hidden = true
    }
    const response = await fetch("state", {
        method: 'GET',
        mode: 'cors',
        cache: 'no-cache',
        credentials: 'same-origin',
        redirect: 'error',
        referrerPolicy: 'no-referrer'
    });
    let initialState
    try {
        initialState = await response.json()
    } catch(e) {
        console.log(e)
    }
    updateTable(initialState)
    drawSeries(initialState)
    const logResponse = await fetch("logs", {
        method: 'GET',
        mode: 'cors',
        cache: 'no-cache',
        credentials: 'same-origin',
        redirect: 'error',
        referrerPolicy: 'no-referrer'
    });
    try {
        initialState = await logResponse.json()
    } catch(e) {
        console.log(e)
    }
    for (let i = initialState.length-1; i >= 0; i--) {
        if (initialState[i].ts === 0) {
            addLogMsg("")
            continue
        }
        addLogMsg(`${new Date(initialState[i].ts*1000).toLocaleTimeString()} - ${initialState[i].msg}`)
    }
}

const blocks = new Map();

function updateTable(status) {
    for (let i = document.getElementById("statusTable").rows.length; i > 0; i--) {
        document.getElementById("statusTable").deleteRow(i-1)
    }

    for (let i = 0; i < status.Status.length; i++) {
        // Alert icon - show when nodes down OR last_error exists OR active_alerts > 0
        let alerts = ""
        const nodesDown = status.Status[i].healthy_nodes < status.Status[i].nodes
        const hasError = status.Status[i].last_error !== ""
        const hasAlerts = status.Status[i].active_alerts > 0

        if (nodesDown || hasError || hasAlerts) {
            // Build descriptive error message
            let errorMsg = status.Status[i].last_error || ""
            if (!errorMsg && nodesDown) {
                const downCount = status.Status[i].nodes - status.Status[i].healthy_nodes
                errorMsg = `${downCount} of ${status.Status[i].nodes} RPC nodes are down`
            }

            alerts = `
            <button class="alert-icon has-tooltip" data-tooltip="${_.escape(errorMsg).replace(/"/g, '&quot;').replace(/\n/g, '&#10;')}" onclick="showModal('${_.escape(errorMsg).replace(/'/g, "\\'")}', 'Issues: ${_.escape(status.Status[i].name)}')">
                <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                    <path d="M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z"></path>
                    <line x1="12" y1="9" x2="12" y2="13"></line>
                    <line x1="12" y1="17" x2="12.01" y2="17"></line>
                </svg>
            </button>`
        }

        // Bonded status badge
        let bonded = ""
        switch (true) {
            case status.Status[i].tombstoned:
                bonded = `<span class="badge badge--tombstoned">
                    <svg xmlns="http://www.w3.org/2000/svg" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                        <circle cx="12" cy="12" r="10"></circle>
                        <line x1="4.93" y1="4.93" x2="19.07" y2="19.07"></line>
                    </svg>
                    TOMBSTONED
                </span>`
                break
            case status.Status[i].jailed:
                bonded = `<span class="badge badge--jailed">
                    <svg xmlns="http://www.w3.org/2000/svg" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                        <path d="M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z"></path>
                        <line x1="12" y1="9" x2="12" y2="13"></line>
                        <line x1="12" y1="17" x2="12.01" y2="17"></line>
                    </svg>
                    JAILED
                </span>`
                break
            case status.Status[i].bonded:
                bonded = `<span class="badge badge--bonded">
                    <svg xmlns="http://www.w3.org/2000/svg" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                        <polyline points="20 6 9 17 4 12"></polyline>
                    </svg>
                    BONDED
                </span>`
                break
            default:
                bonded = `<span class="badge badge--inactive">INACTIVE</span>`
        }

        // Uptime calculation - two column layout like original
        let uptimeHtml = `<div class="uptime-grid">`
        let uptimeClass = ""
        if (status.Status[i].missed === 0 && status.Status[i].window === 0) {
            uptimeHtml += `<span>error</span>`
        } else if (status.Status[i].missed === 0) {
            uptimeHtml += `<span>100%</span>`
        } else {
            const pct = 100 - (status.Status[i].missed / status.Status[i].window) * 100
            if (pct < 95) {
                uptimeClass = "text-warning"
            }
            uptimeHtml += `<span class="${uptimeClass}">${pct.toFixed(2)}%</span>`
        }
        uptimeHtml += `<span class="uptime-detail">${_.escape(status.Status[i].missed)} / ${_.escape(status.Status[i].window)}</span></div>`

        // Node health
        let nodes = `${_.escape(status.Status[i].healthy_nodes)} / ${_.escape(status.Status[i].nodes)}`
        let nodeClass = "cell-nodes"
        if (status.Status[i].healthy_nodes < status.Status[i].nodes) {
            nodeClass += " node-status--degraded"
            nodes = `<span class="node-status node-status--degraded">
                <svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
                    <line x1="12" y1="5" x2="12" y2="19"></line>
                    <polyline points="19 12 12 19 5 12"></polyline>
                </svg>
                ${_.escape(status.Status[i].healthy_nodes)} / ${_.escape(status.Status[i].nodes)}
            </span>`
        }

        // Height with animation class
        let heightClass = "cell-height"
        if (blocks.get(status.Status[i].chain_id) !== status.Status[i].height){
            heightClass += " height-updated"
        }
        blocks.set(status.Status[i].chain_id, status.Status[i].height)

        // Build table row
        let r = document.getElementById('statusTable').insertRow(i)

        // Alert cell
        r.insertCell(0).innerHTML = alerts

        // Chain name with chain_id
        r.insertCell(1).innerHTML = `<div class="cell-chain">${_.escape(status.Status[i].name)}<small>${_.escape(status.Status[i].chain_id)}</small></div>`

        // Height
        r.insertCell(2).innerHTML = `<div class="${heightClass}">${_.escape(status.Status[i].height)}</div>`

        // Moniker
        if (status.Status[i].moniker === "not connected") {
            r.insertCell(3).innerHTML = `<div class="cell-moniker text-warning">${_.escape(status.Status[i].moniker)}</div>`
            bonded = `<span class="badge badge--inactive">UNKNOWN</span>`
        } else {
            r.insertCell(3).innerHTML = `<div class="cell-moniker">${_.escape(status.Status[i].moniker.substring(0,24))}</div>`
        }

        // Bonded status
        r.insertCell(4).innerHTML = bonded

        // Uptime
        r.insertCell(5).innerHTML = uptimeHtml

        // Nodes
        r.insertCell(6).innerHTML = `<div class="${nodeClass}">${nodes}</div>`
    }
}

let logs = new Array(1);

function addLogMsg(str) {
    if (logs.length >= 256) {
        logs.pop()
    }
    logs.unshift(str)
    if (document.visibilityState !== "hidden") {
        document.getElementById("logs").innerText = logs.join("\n")
    }
}

function connect() {
    let wsProto = "ws://"
    if (location.protocol === "https:") {
        wsProto = "wss://"
    }
    const parse = function (event) {
        const msg = JSON.parse(event.data);
        if (msg.msgType === "log"){
            addLogMsg(`${new Date(msg.ts*1000).toLocaleTimeString()} - ${msg.msg}`)
        } else if (msg.msgType === "update" && document.visibilityState !== "hidden"){
            updateTable(msg)
            drawSeries(msg)
        }
        event = null
    }
    const socket = new WebSocket(wsProto + location.host + '/ws');
    socket.addEventListener('message', function (event) {parse(event)});
    socket.onclose = function(e) {
        console.log('Socket is closed, retrying /ws ...', e.reason);
        addLogMsg('Socket is closed, retrying /ws ...' + e.reason)
        setTimeout(function() {
            connect();
        }, 3000);
    };
}
connect()
