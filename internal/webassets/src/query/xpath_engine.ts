import { domFacade, evaluateXPathToNodes, type IDomFacade } from "fontoxpath";

function normalizeXPathSelector(selector: string): string {
    let cleanSelector = selector;
    if (selector.startsWith('xpath/')) {
        cleanSelector = selector.slice(6);
    } else if (selector.startsWith('xpath=')) {
        cleanSelector = selector.slice(6);
    }

    if (cleanSelector.startsWith('//')) {
        return '.' + cleanSelector;
    }
    if (cleanSelector.startsWith('(')) {
        return cleanSelector.replace(/^\((\s*)\/\//, '($1.//');
    }
    return cleanSelector;
}

export function isDocumentAbsoluteXPath(selector: string): boolean {
    let cleanSelector = selector.trimStart();
    if (cleanSelector.startsWith('xpath=') || cleanSelector.startsWith('xpath/')) {
        cleanSelector = cleanSelector.slice(6).trimStart();
    }
    return /^\/(?!\/)/.test(cleanSelector) || /^\(\s*\/(?!\/)/.test(cleanSelector);
}

export function evaluateXPathElements(
    root: Element | Document | ShadowRoot,
    selector: string,
): Element[] {
    if (isDocumentAbsoluteXPath(selector) && root.nodeType !== Node.DOCUMENT_NODE) {
        return [];
    }
    const normalizedSelector = normalizeXPathSelector(selector);
    let context: Element | Document = root as Element | Document;
    let facade: IDomFacade | null = null;
    if (root instanceof ShadowRoot) {
        const document = root.ownerDocument;
        facade = Object.create(domFacade) as IDomFacade;
        facade.getChildNodes = (node, bucket) => node === document
            ? domFacade.getChildNodes(root, bucket)
            : domFacade.getChildNodes(node, bucket);
        facade.getFirstChild = (node, bucket) => node === document
            ? domFacade.getFirstChild(root, bucket)
            : domFacade.getFirstChild(node, bucket);
        facade.getLastChild = (node, bucket) => node === document
            ? domFacade.getLastChild(root, bucket)
            : domFacade.getLastChild(node, bucket);
        facade.getParentNode = (node, bucket) => {
            const parent = domFacade.getParentNode(node, bucket);
            return parent === root ? document : parent;
        };
        context = document;
    }
    const matches = evaluateXPathToNodes(normalizedSelector, context, facade);
    return matches.filter((node): node is Element => {
        return !!node && typeof node === 'object' && 'nodeType' in node && node.nodeType === 1;
    });
}
