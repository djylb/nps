import './style.css';
import './app.css';

import {
    GetVersion,
    ListConfigs,
    SaveConfig,
    DeleteConfig,
    StartClient,
    StopClient,
    GetClientStatus,
    GetLogs,
    ClearLogs
} from '../wailsjs/go/main/App';

let currentConfigs = [];
let currentView = 'list';
let editingConfig = null;
let logInterval = null;

// Initialize the app
document.addEventListener('DOMContentLoaded', () => {
    renderApp();
    loadConfigs();
    startLogPolling();
});

function renderApp() {
    document.querySelector('#app').innerHTML = `
        <div class="container">
            <header class="header">
                <h1>NPC GUI - NPS Client Manager</h1>
                <div id="version" class="version">Loading...</div>
            </header>
            
            <nav class="nav">
                <button class="nav-btn active" onclick="window.switchView('list')">Configurations</button>
                <button class="nav-btn" onclick="window.switchView('logs')">Logs</button>
            </nav>
            
            <main class="main-content">
                <div id="content"></div>
            </main>
        </div>
    `;

    // Load version
    GetVersion().then(version => {
        document.getElementById('version').textContent = version;
    }).catch(err => {
        console.error('Failed to get version:', err);
    });
}

function renderConfigList() {
    const content = document.getElementById('content');
    content.innerHTML = `
        <div class="config-list">
            <div class="list-header">
                <h2>Client Configurations</h2>
                <button class="btn btn-primary" onclick="window.showConfigForm()">+ Add New</button>
            </div>
            <div class="info-banner">
                <strong>📌 提示 / Tip:</strong> 连接成功后，请访问 <a href="https://jqhl.jqcloudnet.cn" target="_blank">jqhl.jqcloudnet.cn</a> 登录并创建隧道。<br>
                After successful connection, please visit <a href="https://jqhl.jqcloudnet.cn" target="_blank">jqhl.jqcloudnet.cn</a> to login and create tunnels.
            </div>
            <div id="config-items" class="config-items">
                ${currentConfigs.length === 0 ? '<p class="empty-message">No configurations yet. Click "Add New" to create one.</p>' : ''}
            </div>
        </div>
    `;

    if (currentConfigs.length > 0) {
        const itemsContainer = document.getElementById('config-items');
        currentConfigs.forEach(config => {
            const item = createConfigItem(config);
            itemsContainer.appendChild(item);
        });
    }
}

function createConfigItem(config) {
    const div = document.createElement('div');
    div.className = 'config-item';
    div.innerHTML = `
        <div class="config-info">
            <h3>${escapeHtml(config.name)}</h3>
            <div class="config-details">
                <span><strong>Server:</strong> ${escapeHtml(config.serverAddr)}</span>
                <span><strong>Type:</strong> ${escapeHtml(config.connType)}</span>
                <span class="status ${config.isActive ? 'status-active' : 'status-inactive'}">
                    ${config.isActive ? '● Running' : '○ Stopped'}
                </span>
            </div>
        </div>
        <div class="config-actions">
            <button class="btn btn-sm ${config.isActive ? 'btn-danger' : 'btn-success'}" 
                    onclick="window.toggleClient('${config.id}', ${config.isActive})">
                ${config.isActive ? 'Stop' : 'Start'}
            </button>
            <button class="btn btn-sm btn-secondary" onclick="window.editConfig('${config.id}')">Edit</button>
            <button class="btn btn-sm btn-danger" onclick="window.deleteConfig('${config.id}')">Delete</button>
        </div>
    `;
    return div;
}

function renderConfigForm() {
    const content = document.getElementById('content');
    const isEdit = editingConfig !== null;
    const config = editingConfig || {
        name: '',
        serverAddr: '',
        vkey: '',
        connType: 'tcp',
        proxyUrl: '',
        logLevel: 'info',
        autoReconnect: true,
        skipVerify: false,
        disableP2P: false,
        protoVersion: 2,
        dnsServer: '8.8.8.8',
        keepAlive: 0
    };
    
    // Extract hostname without port for display in edit mode
    let displayAddr = config.serverAddr;
    if (displayAddr && displayAddr.includes(':')) {
        displayAddr = displayAddr.split(':')[0];
    }

    content.innerHTML = `
        <div class="config-form">
            <div class="form-header">
                <h2>${isEdit ? 'Edit' : 'Add New'} Configuration</h2>
                <button class="btn btn-secondary" onclick="window.cancelConfigForm()">Cancel</button>
            </div>
            <form id="configForm" onsubmit="window.submitConfigForm(event)">
                <div class="form-group">
                    <label for="name">Configuration Name *</label>
                    <input type="text" id="name" name="name" value="${escapeHtml(config.name)}" required>
                </div>
                
                <div class="form-group">
                    <label for="serverAddr">Server Address *</label>
                    <input type="text" id="serverAddr" name="serverAddr" 
                           value="${escapeHtml(displayAddr)}" 
                           placeholder="example.com" required>
                    <small>Enter server domain or IP (port will use default: 8024)</small>
                </div>
                
                <div class="form-group">
                    <label for="vkey">Verification Key *</label>
                    <input type="text" id="vkey" name="vkey" 
                           value="${escapeHtml(config.vkey)}" required>
                </div>
                
                <div class="form-row">
                    <div class="form-group">
                        <label for="connType">Connection Type</label>
                        <select id="connType" name="connType">
                            <option value="tcp" ${config.connType === 'tcp' ? 'selected' : ''}>TCP</option>
                            <option value="tls" ${config.connType === 'tls' ? 'selected' : ''}>TLS</option>
                            <option value="kcp" ${config.connType === 'kcp' ? 'selected' : ''}>KCP</option>
                            <option value="quic" ${config.connType === 'quic' ? 'selected' : ''}>QUIC</option>
                            <option value="ws" ${config.connType === 'ws' ? 'selected' : ''}>WebSocket</option>
                            <option value="wss" ${config.connType === 'wss' ? 'selected' : ''}>WebSocket TLS</option>
                        </select>
                    </div>
                    
                    <div class="form-group">
                        <label for="logLevel">Log Level</label>
                        <select id="logLevel" name="logLevel">
                            <option value="trace" ${config.logLevel === 'trace' ? 'selected' : ''}>Trace</option>
                            <option value="debug" ${config.logLevel === 'debug' ? 'selected' : ''}>Debug</option>
                            <option value="info" ${config.logLevel === 'info' ? 'selected' : ''}>Info</option>
                            <option value="warn" ${config.logLevel === 'warn' ? 'selected' : ''}>Warn</option>
                            <option value="error" ${config.logLevel === 'error' ? 'selected' : ''}>Error</option>
                        </select>
                    </div>
                </div>
                
                <div class="form-group">
                    <label for="proxyUrl">Proxy URL (Optional)</label>
                    <input type="text" id="proxyUrl" name="proxyUrl" 
                           value="${escapeHtml(config.proxyUrl)}" 
                           placeholder="socks5://user:pass@127.0.0.1:9007">
                </div>
                
                <div class="form-row">
                    <div class="form-group">
                        <label for="dnsServer">DNS Server</label>
                        <input type="text" id="dnsServer" name="dnsServer" 
                               value="${escapeHtml(config.dnsServer)}">
                    </div>
                    
                    <div class="form-group">
                        <label for="keepAlive">Keep Alive (seconds, 0=default)</label>
                        <input type="number" id="keepAlive" name="keepAlive" 
                               value="${config.keepAlive}" min="0">
                    </div>
                </div>
                
                <div class="form-group checkbox-group">
                    <label>
                        <input type="checkbox" id="autoReconnect" name="autoReconnect" 
                               ${config.autoReconnect ? 'checked' : ''}>
                        Auto Reconnect
                    </label>
                    <label>
                        <input type="checkbox" id="skipVerify" name="skipVerify" 
                               ${config.skipVerify ? 'checked' : ''}>
                        Skip TLS Verification
                    </label>
                    <label>
                        <input type="checkbox" id="disableP2P" name="disableP2P" 
                               ${config.disableP2P ? 'checked' : ''}>
                        Disable P2P
                    </label>
                </div>
                
                <div class="form-actions">
                    <button type="submit" class="btn btn-primary">Save Configuration</button>
                    <button type="button" class="btn btn-secondary" onclick="window.cancelConfigForm()">Cancel</button>
                </div>
            </form>
        </div>
    `;
}

function renderLogs() {
    const content = document.getElementById('content');
    content.innerHTML = `
        <div class="logs-view">
            <div class="logs-header">
                <h2>Client Logs</h2>
                <button class="btn btn-secondary" onclick="window.clearLogsView()">Clear Logs</button>
            </div>
            <div id="logs-content" class="logs-content">
                <p class="empty-message">Loading logs...</p>
            </div>
        </div>
    `;
    updateLogs();
}

function updateLogs() {
    GetLogs().then(logs => {
        const logsContent = document.getElementById('logs-content');
        if (!logsContent) return;

        if (logs.length === 0) {
            logsContent.innerHTML = '<p class="empty-message">No logs yet.</p>';
        } else {
            logsContent.innerHTML = logs.map(log => 
                `<div class="log-entry">${escapeHtml(log)}</div>`
            ).join('');
            logsContent.scrollTop = logsContent.scrollHeight;
        }
    }).catch(err => {
        console.error('Failed to get logs:', err);
    });
}

// Global functions
window.switchView = function(view) {
    currentView = view;
    
    // Update nav buttons
    document.querySelectorAll('.nav-btn').forEach(btn => {
        btn.classList.remove('active');
    });
    event.target.classList.add('active');
    
    if (view === 'list') {
        renderConfigList();
    } else if (view === 'logs') {
        renderLogs();
    }
};

window.showConfigForm = function() {
    editingConfig = null;
    renderConfigForm();
};

window.editConfig = function(id) {
    editingConfig = currentConfigs.find(c => c.id === id);
    if (editingConfig) {
        renderConfigForm();
    }
};

window.cancelConfigForm = function() {
    editingConfig = null;
    renderConfigList();
};

window.submitConfigForm = function(event) {
    event.preventDefault();
    
    const formData = new FormData(event.target);
    let serverAddr = formData.get('serverAddr').trim();
    
    // If server address doesn't contain a port, append default port 8024
    if (!serverAddr.includes(':')) {
        serverAddr = serverAddr + ':8024';
    }
    
    const config = {
        id: editingConfig ? editingConfig.id : '',
        name: formData.get('name'),
        serverAddr: serverAddr,
        vkey: formData.get('vkey'),
        connType: formData.get('connType'),
        proxyUrl: formData.get('proxyUrl'),
        logLevel: formData.get('logLevel'),
        autoReconnect: formData.get('autoReconnect') === 'on',
        skipVerify: formData.get('skipVerify') === 'on',
        disableP2P: formData.get('disableP2P') === 'on',
        protoVersion: 2,
        dnsServer: formData.get('dnsServer'),
        keepAlive: parseInt(formData.get('keepAlive')) || 0
    };
    
    SaveConfig(config).then(() => {
        editingConfig = null;
        loadConfigs();
        renderConfigList();
    }).catch(err => {
        alert('Failed to save configuration: ' + err);
    });
};

window.deleteConfig = function(id) {
    if (confirm('Are you sure you want to delete this configuration?')) {
        DeleteConfig(id).then(() => {
            loadConfigs();
        }).catch(err => {
            alert('Failed to delete configuration: ' + err);
        });
    }
};

window.toggleClient = function(id, isActive) {
    const action = isActive ? StopClient : StartClient;
    action(id).then(() => {
        setTimeout(() => loadConfigs(), 500);
        // Show tunnel creation info when starting a client
        if (!isActive) {
            setTimeout(() => {
                alert('连接成功！\n\n要创建隧道，请访问：https://jqhl.jqcloudnet.cn 进行登录和配置。\n\nConnection successful!\n\nTo create tunnels, please visit: https://jqhl.jqcloudnet.cn to login and configure.');
            }, 1000);
        }
    }).catch(err => {
        alert(`Failed to ${isActive ? 'stop' : 'start'} client: ` + err);
    });
};

window.clearLogsView = function() {
    ClearLogs().then(() => {
        updateLogs();
    }).catch(err => {
        console.error('Failed to clear logs:', err);
    });
};

function loadConfigs() {
    ListConfigs().then(configs => {
        currentConfigs = configs || [];
        if (currentView === 'list') {
            renderConfigList();
        }
    }).catch(err => {
        console.error('Failed to load configs:', err);
    });
}

function startLogPolling() {
    if (logInterval) {
        clearInterval(logInterval);
    }
    logInterval = setInterval(() => {
        if (currentView === 'logs') {
            updateLogs();
        }
    }, 2000);
}

function escapeHtml(text) {
    if (!text) return '';
    const div = document.createElement('div');
    div.textContent = text;
    return div.innerHTML;
}
