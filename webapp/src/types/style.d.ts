// Copyright (c) 2015-present Mattermost, Inc. All Rights Reserved.
// See LICENSE.txt for license information.

// CSS Modules: `import styles from './x.module.scss'` yields a map of authored
// class names to their hashed, locally-scoped equivalents.
declare module '*.module.scss' {
    const classes: {readonly [key: string]: string};
    export default classes;
}

declare module '*.module.css' {
    const classes: {readonly [key: string]: string};
    export default classes;
}

// Fallback for any non-module global stylesheet imported for side effects.
declare module '*.scss';
declare module '*.css';
