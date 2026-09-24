import {DESIGN_TOKENS} from "../design";
import {applyNonInteractive, ensureOverlayHost, removeElement} from "../canvas/dom";
import {internalElementID} from "../utils/internal_ids";

/**
 * 告警数据接口
 */
interface AlertData {
    msg: string;
    timeout?: number; // 毫秒
}
const ALERT_ID = internalElementID('run_alert_container');
const STYLE_ID = internalElementID('run_alert_style');
const ALERT_HOST_ID = internalElementID('run_alert_host');
let alertTimer: ReturnType<typeof setTimeout> | null = null;
let alertHost: HTMLDivElement | null = null;
let alertRoot: ShadowRoot | null = null;

const ensureAlertRoot = (): ShadowRoot => {
    if (alertRoot && alertHost?.isConnected) {
        return alertRoot;
    }
    const ensured = ensureOverlayHost(alertHost, ALERT_HOST_ID, DESIGN_TOKENS.zToast);
    alertHost = ensured.host;
    alertRoot = ensured.root;
    return ensured.root;
};

/**
 * 注入样式表（单例模式，只添加一次）
 */
const injectStyles = (): void => {
    const root = ensureAlertRoot();
    if (root.getElementById(STYLE_ID)) return;

    const style = document.createElement('style');
    style.id = STYLE_ID;
    style.innerHTML = `
        @keyframes shake {
            0% { transform: translateX(0); }
            25% { transform: translateX(-5px); }
            50% { transform: translateX(5px); }
            75% { transform: translateX(-5px); }
            100% { transform: translateX(0); }
        }
        .shake-text {
            animation: shake 0.5s infinite;
            position: fixed;
            top: 20px;
            font-size: 18px;
            left: 50px;
            right: 50px;
            color: red;
            padding: 10px;
            background-color: #333;
            border-radius: 5px;
            z-index: 1000;
            text-align: center;
            box-shadow: 0 4px 15px rgba(0,0,0,0.3);
            pointer-events: none !important; /* 防止遮挡点击事件 */
        }
    `;
    root.appendChild(style);
};

/**
 * 显示告警：如果已存在则不重复添加
 */
export const showAlert = (data: AlertData): void => {
    const root = ensureAlertRoot();
    // 1. 检查是否已经存在告警
    if (root.getElementById(ALERT_ID)) {
        return;
    }

    // 2. 注入样式
    injectStyles();

    // 3. 创建并插入元素
    const alertDiv = document.createElement('div');
    alertDiv.className = 'shake-text';
    alertDiv.id = ALERT_ID;
    alertDiv.innerText = data.msg;
    applyNonInteractive(alertDiv);
    root.appendChild(alertDiv);

    // 4. 定时自动移除
    if (data.timeout && data.timeout > 0) {
        if (alertTimer) {
            clearTimeout(alertTimer);
        }
        alertTimer = setTimeout(() => {
            alertTimer = null;
            removeAlert();
        }, data.timeout);
    }
};

/**
 * 移除告警
 */
export const removeAlert = (): void => {
    if (alertTimer) {
        clearTimeout(alertTimer);
        alertTimer = null;
    }
    const element = alertRoot?.getElementById(ALERT_ID);
    if (element) {
        element.remove(); // 直接从 DOM 中移除，比隐藏更干净
    }
};

export const cleanupAlert = (): void => {
    removeAlert();
    alertRoot?.getElementById(STYLE_ID)?.remove();
    removeElement(alertHost);
    alertHost = null;
    alertRoot = null;
};
