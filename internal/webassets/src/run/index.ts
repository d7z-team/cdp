import {cleanupAlert, removeAlert, showAlert} from "./editor";
import type {ICdpRun} from "../api/types";

export type {ICdpRun};

export function createRun(): ICdpRun {
    return {
        alertAdd(msg: string, timeout?: number) {
            showAlert({msg, timeout});
        },
        alertRemove() {
            removeAlert();
        },
        destroy() {
            cleanupAlert();
        },
    };
}
