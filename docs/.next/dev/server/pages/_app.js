/*
 * ATTENTION: An "eval-source-map" devtool has been used.
 * This devtool is neither made for production nor for readable output files.
 * It uses "eval()" calls to create a separate source file with attached SourceMaps in the browser devtools.
 * If you are trying to read the output file, select a different devtool (https://webpack.js.org/configuration/devtool/)
 * or disable the default devtool with "devtool: false".
 * If you are looking for production-ready output files, see mode: "production" (https://webpack.js.org/configuration/mode/).
 */
(() => {
var exports = {};
exports.id = "pages/_app";
exports.ids = ["pages/_app"];
exports.modules = {

/***/ "(pages-dir-node)/./components/Layout.js":
/*!******************************!*\
  !*** ./components/Layout.js ***!
  \******************************/
/***/ ((__unused_webpack_module, __webpack_exports__, __webpack_require__) => {

"use strict";
eval("__webpack_require__.r(__webpack_exports__);\n/* harmony export */ __webpack_require__.d(__webpack_exports__, {\n/* harmony export */   \"default\": () => (/* binding */ Layout)\n/* harmony export */ });\n/* harmony import */ var react_jsx_dev_runtime__WEBPACK_IMPORTED_MODULE_0__ = __webpack_require__(/*! react/jsx-dev-runtime */ \"react/jsx-dev-runtime\");\n/* harmony import */ var react_jsx_dev_runtime__WEBPACK_IMPORTED_MODULE_0___default = /*#__PURE__*/__webpack_require__.n(react_jsx_dev_runtime__WEBPACK_IMPORTED_MODULE_0__);\n/* harmony import */ var next_link__WEBPACK_IMPORTED_MODULE_1__ = __webpack_require__(/*! next/link */ \"(pages-dir-node)/./node_modules/next/link.js\");\n/* harmony import */ var next_link__WEBPACK_IMPORTED_MODULE_1___default = /*#__PURE__*/__webpack_require__.n(next_link__WEBPACK_IMPORTED_MODULE_1__);\n/* harmony import */ var next_router__WEBPACK_IMPORTED_MODULE_2__ = __webpack_require__(/*! next/router */ \"(pages-dir-node)/./node_modules/next/router.js\");\n/* harmony import */ var next_router__WEBPACK_IMPORTED_MODULE_2___default = /*#__PURE__*/__webpack_require__.n(next_router__WEBPACK_IMPORTED_MODULE_2__);\n\n\n\nconst navigation = [\n    {\n        title: 'Getting Started',\n        items: [\n            {\n                title: 'Introduction',\n                href: '/'\n            },\n            {\n                title: 'Installation',\n                href: '/getting-started/installation'\n            },\n            {\n                title: 'Quick Start',\n                href: '/getting-started/quickstart'\n            }\n        ]\n    },\n    {\n        title: 'CLI Commands',\n        items: [\n            {\n                title: 'verify',\n                href: '/cli/verify'\n            },\n            {\n                title: 'chat',\n                href: '/cli/chat'\n            },\n            {\n                title: 'run',\n                href: '/cli/run'\n            },\n            {\n                title: 'vars',\n                href: '/cli/vars'\n            }\n        ]\n    },\n    {\n        title: 'Configuration',\n        items: [\n            {\n                title: 'Overview',\n                href: '/config/overview'\n            },\n            {\n                title: 'Variables',\n                href: '/config/variables'\n            },\n            {\n                title: 'Models',\n                href: '/config/models'\n            },\n            {\n                title: 'Agents',\n                href: '/config/agents'\n            },\n            {\n                title: 'Tools',\n                href: '/config/tools'\n            },\n            {\n                title: 'Plugins',\n                href: '/config/plugins'\n            }\n        ]\n    },\n    {\n        title: 'Workflows',\n        items: [\n            {\n                title: 'Overview',\n                href: '/workflows/overview'\n            },\n            {\n                title: 'Tasks',\n                href: '/workflows/tasks'\n            },\n            {\n                title: 'Datasets',\n                href: '/workflows/datasets'\n            },\n            {\n                title: 'Iteration',\n                href: '/workflows/iteration'\n            },\n            {\n                title: 'Internal Tools',\n                href: '/workflows/internal-tools'\n            }\n        ]\n    }\n];\nfunction Layout({ children }) {\n    const router = (0,next_router__WEBPACK_IMPORTED_MODULE_2__.useRouter)();\n    return /*#__PURE__*/ (0,react_jsx_dev_runtime__WEBPACK_IMPORTED_MODULE_0__.jsxDEV)(\"div\", {\n        className: \"layout\",\n        children: [\n            /*#__PURE__*/ (0,react_jsx_dev_runtime__WEBPACK_IMPORTED_MODULE_0__.jsxDEV)(\"aside\", {\n                className: \"sidebar\",\n                children: [\n                    /*#__PURE__*/ (0,react_jsx_dev_runtime__WEBPACK_IMPORTED_MODULE_0__.jsxDEV)((next_link__WEBPACK_IMPORTED_MODULE_1___default()), {\n                        href: \"/\",\n                        className: \"sidebar-logo\",\n                        children: \"Squad\"\n                    }, void 0, false, {\n                        fileName: \"/Users/maxlund/projects/floze/docs/components/Layout.js\",\n                        lineNumber: 51,\n                        columnNumber: 9\n                    }, this),\n                    /*#__PURE__*/ (0,react_jsx_dev_runtime__WEBPACK_IMPORTED_MODULE_0__.jsxDEV)(\"nav\", {\n                        children: /*#__PURE__*/ (0,react_jsx_dev_runtime__WEBPACK_IMPORTED_MODULE_0__.jsxDEV)(\"ul\", {\n                            children: navigation.map((section)=>/*#__PURE__*/ (0,react_jsx_dev_runtime__WEBPACK_IMPORTED_MODULE_0__.jsxDEV)(\"li\", {\n                                    children: [\n                                        /*#__PURE__*/ (0,react_jsx_dev_runtime__WEBPACK_IMPORTED_MODULE_0__.jsxDEV)(\"span\", {\n                                            className: \"section-title\",\n                                            children: section.title\n                                        }, void 0, false, {\n                                            fileName: \"/Users/maxlund/projects/floze/docs/components/Layout.js\",\n                                            lineNumber: 58,\n                                            columnNumber: 17\n                                        }, this),\n                                        /*#__PURE__*/ (0,react_jsx_dev_runtime__WEBPACK_IMPORTED_MODULE_0__.jsxDEV)(\"ul\", {\n                                            children: section.items.map((item)=>/*#__PURE__*/ (0,react_jsx_dev_runtime__WEBPACK_IMPORTED_MODULE_0__.jsxDEV)(\"li\", {\n                                                    children: /*#__PURE__*/ (0,react_jsx_dev_runtime__WEBPACK_IMPORTED_MODULE_0__.jsxDEV)((next_link__WEBPACK_IMPORTED_MODULE_1___default()), {\n                                                        href: item.href,\n                                                        className: router.pathname === item.href ? 'active' : '',\n                                                        children: item.title\n                                                    }, void 0, false, {\n                                                        fileName: \"/Users/maxlund/projects/floze/docs/components/Layout.js\",\n                                                        lineNumber: 62,\n                                                        columnNumber: 23\n                                                    }, this)\n                                                }, item.href, false, {\n                                                    fileName: \"/Users/maxlund/projects/floze/docs/components/Layout.js\",\n                                                    lineNumber: 61,\n                                                    columnNumber: 21\n                                                }, this))\n                                        }, void 0, false, {\n                                            fileName: \"/Users/maxlund/projects/floze/docs/components/Layout.js\",\n                                            lineNumber: 59,\n                                            columnNumber: 17\n                                        }, this)\n                                    ]\n                                }, section.title, true, {\n                                    fileName: \"/Users/maxlund/projects/floze/docs/components/Layout.js\",\n                                    lineNumber: 57,\n                                    columnNumber: 15\n                                }, this))\n                        }, void 0, false, {\n                            fileName: \"/Users/maxlund/projects/floze/docs/components/Layout.js\",\n                            lineNumber: 55,\n                            columnNumber: 11\n                        }, this)\n                    }, void 0, false, {\n                        fileName: \"/Users/maxlund/projects/floze/docs/components/Layout.js\",\n                        lineNumber: 54,\n                        columnNumber: 9\n                    }, this)\n                ]\n            }, void 0, true, {\n                fileName: \"/Users/maxlund/projects/floze/docs/components/Layout.js\",\n                lineNumber: 50,\n                columnNumber: 7\n            }, this),\n            /*#__PURE__*/ (0,react_jsx_dev_runtime__WEBPACK_IMPORTED_MODULE_0__.jsxDEV)(\"main\", {\n                className: \"main-content\",\n                children: /*#__PURE__*/ (0,react_jsx_dev_runtime__WEBPACK_IMPORTED_MODULE_0__.jsxDEV)(\"article\", {\n                    className: \"content\",\n                    children: children\n                }, void 0, false, {\n                    fileName: \"/Users/maxlund/projects/floze/docs/components/Layout.js\",\n                    lineNumber: 77,\n                    columnNumber: 9\n                }, this)\n            }, void 0, false, {\n                fileName: \"/Users/maxlund/projects/floze/docs/components/Layout.js\",\n                lineNumber: 76,\n                columnNumber: 7\n            }, this)\n        ]\n    }, void 0, true, {\n        fileName: \"/Users/maxlund/projects/floze/docs/components/Layout.js\",\n        lineNumber: 49,\n        columnNumber: 5\n    }, this);\n}\n//# sourceURL=[module]\n//# sourceMappingURL=data:application/json;charset=utf-8;base64,eyJ2ZXJzaW9uIjozLCJmaWxlIjoiKHBhZ2VzLWRpci1ub2RlKS8uL2NvbXBvbmVudHMvTGF5b3V0LmpzIiwibWFwcGluZ3MiOiI7Ozs7Ozs7Ozs7O0FBQTZCO0FBQ1c7QUFFeEMsTUFBTUUsYUFBYTtJQUNqQjtRQUNFQyxPQUFPO1FBQ1BDLE9BQU87WUFDTDtnQkFBRUQsT0FBTztnQkFBZ0JFLE1BQU07WUFBSTtZQUNuQztnQkFBRUYsT0FBTztnQkFBZ0JFLE1BQU07WUFBZ0M7WUFDL0Q7Z0JBQUVGLE9BQU87Z0JBQWVFLE1BQU07WUFBOEI7U0FDN0Q7SUFDSDtJQUNBO1FBQ0VGLE9BQU87UUFDUEMsT0FBTztZQUNMO2dCQUFFRCxPQUFPO2dCQUFVRSxNQUFNO1lBQWM7WUFDdkM7Z0JBQUVGLE9BQU87Z0JBQVFFLE1BQU07WUFBWTtZQUNuQztnQkFBRUYsT0FBTztnQkFBT0UsTUFBTTtZQUFXO1lBQ2pDO2dCQUFFRixPQUFPO2dCQUFRRSxNQUFNO1lBQVk7U0FDcEM7SUFDSDtJQUNBO1FBQ0VGLE9BQU87UUFDUEMsT0FBTztZQUNMO2dCQUFFRCxPQUFPO2dCQUFZRSxNQUFNO1lBQW1CO1lBQzlDO2dCQUFFRixPQUFPO2dCQUFhRSxNQUFNO1lBQW9CO1lBQ2hEO2dCQUFFRixPQUFPO2dCQUFVRSxNQUFNO1lBQWlCO1lBQzFDO2dCQUFFRixPQUFPO2dCQUFVRSxNQUFNO1lBQWlCO1lBQzFDO2dCQUFFRixPQUFPO2dCQUFTRSxNQUFNO1lBQWdCO1lBQ3hDO2dCQUFFRixPQUFPO2dCQUFXRSxNQUFNO1lBQWtCO1NBQzdDO0lBQ0g7SUFDQTtRQUNFRixPQUFPO1FBQ1BDLE9BQU87WUFDTDtnQkFBRUQsT0FBTztnQkFBWUUsTUFBTTtZQUFzQjtZQUNqRDtnQkFBRUYsT0FBTztnQkFBU0UsTUFBTTtZQUFtQjtZQUMzQztnQkFBRUYsT0FBTztnQkFBWUUsTUFBTTtZQUFzQjtZQUNqRDtnQkFBRUYsT0FBTztnQkFBYUUsTUFBTTtZQUF1QjtZQUNuRDtnQkFBRUYsT0FBTztnQkFBa0JFLE1BQU07WUFBNEI7U0FDOUQ7SUFDSDtDQUNEO0FBRWMsU0FBU0MsT0FBTyxFQUFFQyxRQUFRLEVBQUU7SUFDekMsTUFBTUMsU0FBU1Asc0RBQVNBO0lBRXhCLHFCQUNFLDhEQUFDUTtRQUFJQyxXQUFVOzswQkFDYiw4REFBQ0M7Z0JBQU1ELFdBQVU7O2tDQUNmLDhEQUFDVixrREFBSUE7d0JBQUNLLE1BQUs7d0JBQUlLLFdBQVU7a0NBQWU7Ozs7OztrQ0FHeEMsOERBQUNFO2tDQUNDLDRFQUFDQztzQ0FDRVgsV0FBV1ksR0FBRyxDQUFDLENBQUNDLHdCQUNmLDhEQUFDQzs7c0RBQ0MsOERBQUNDOzRDQUFLUCxXQUFVO3NEQUFpQkssUUFBUVosS0FBSzs7Ozs7O3NEQUM5Qyw4REFBQ1U7c0RBQ0VFLFFBQVFYLEtBQUssQ0FBQ1UsR0FBRyxDQUFDLENBQUNJLHFCQUNsQiw4REFBQ0Y7OERBQ0MsNEVBQUNoQixrREFBSUE7d0RBQ0hLLE1BQU1hLEtBQUtiLElBQUk7d0RBQ2ZLLFdBQVdGLE9BQU9XLFFBQVEsS0FBS0QsS0FBS2IsSUFBSSxHQUFHLFdBQVc7a0VBRXJEYSxLQUFLZixLQUFLOzs7Ozs7bURBTE5lLEtBQUtiLElBQUk7Ozs7Ozs7Ozs7O21DQUpmVSxRQUFRWixLQUFLOzs7Ozs7Ozs7Ozs7Ozs7Ozs7Ozs7MEJBbUI5Qiw4REFBQ2lCO2dCQUFLVixXQUFVOzBCQUNkLDRFQUFDVztvQkFBUVgsV0FBVTs4QkFBV0g7Ozs7Ozs7Ozs7Ozs7Ozs7O0FBSXRDIiwic291cmNlcyI6WyIvVXNlcnMvbWF4bHVuZC9wcm9qZWN0cy9mbG96ZS9kb2NzL2NvbXBvbmVudHMvTGF5b3V0LmpzIl0sInNvdXJjZXNDb250ZW50IjpbImltcG9ydCBMaW5rIGZyb20gJ25leHQvbGluayc7XG5pbXBvcnQgeyB1c2VSb3V0ZXIgfSBmcm9tICduZXh0L3JvdXRlcic7XG5cbmNvbnN0IG5hdmlnYXRpb24gPSBbXG4gIHtcbiAgICB0aXRsZTogJ0dldHRpbmcgU3RhcnRlZCcsXG4gICAgaXRlbXM6IFtcbiAgICAgIHsgdGl0bGU6ICdJbnRyb2R1Y3Rpb24nLCBocmVmOiAnLycgfSxcbiAgICAgIHsgdGl0bGU6ICdJbnN0YWxsYXRpb24nLCBocmVmOiAnL2dldHRpbmctc3RhcnRlZC9pbnN0YWxsYXRpb24nIH0sXG4gICAgICB7IHRpdGxlOiAnUXVpY2sgU3RhcnQnLCBocmVmOiAnL2dldHRpbmctc3RhcnRlZC9xdWlja3N0YXJ0JyB9LFxuICAgIF0sXG4gIH0sXG4gIHtcbiAgICB0aXRsZTogJ0NMSSBDb21tYW5kcycsXG4gICAgaXRlbXM6IFtcbiAgICAgIHsgdGl0bGU6ICd2ZXJpZnknLCBocmVmOiAnL2NsaS92ZXJpZnknIH0sXG4gICAgICB7IHRpdGxlOiAnY2hhdCcsIGhyZWY6ICcvY2xpL2NoYXQnIH0sXG4gICAgICB7IHRpdGxlOiAncnVuJywgaHJlZjogJy9jbGkvcnVuJyB9LFxuICAgICAgeyB0aXRsZTogJ3ZhcnMnLCBocmVmOiAnL2NsaS92YXJzJyB9LFxuICAgIF0sXG4gIH0sXG4gIHtcbiAgICB0aXRsZTogJ0NvbmZpZ3VyYXRpb24nLFxuICAgIGl0ZW1zOiBbXG4gICAgICB7IHRpdGxlOiAnT3ZlcnZpZXcnLCBocmVmOiAnL2NvbmZpZy9vdmVydmlldycgfSxcbiAgICAgIHsgdGl0bGU6ICdWYXJpYWJsZXMnLCBocmVmOiAnL2NvbmZpZy92YXJpYWJsZXMnIH0sXG4gICAgICB7IHRpdGxlOiAnTW9kZWxzJywgaHJlZjogJy9jb25maWcvbW9kZWxzJyB9LFxuICAgICAgeyB0aXRsZTogJ0FnZW50cycsIGhyZWY6ICcvY29uZmlnL2FnZW50cycgfSxcbiAgICAgIHsgdGl0bGU6ICdUb29scycsIGhyZWY6ICcvY29uZmlnL3Rvb2xzJyB9LFxuICAgICAgeyB0aXRsZTogJ1BsdWdpbnMnLCBocmVmOiAnL2NvbmZpZy9wbHVnaW5zJyB9LFxuICAgIF0sXG4gIH0sXG4gIHtcbiAgICB0aXRsZTogJ1dvcmtmbG93cycsXG4gICAgaXRlbXM6IFtcbiAgICAgIHsgdGl0bGU6ICdPdmVydmlldycsIGhyZWY6ICcvd29ya2Zsb3dzL292ZXJ2aWV3JyB9LFxuICAgICAgeyB0aXRsZTogJ1Rhc2tzJywgaHJlZjogJy93b3JrZmxvd3MvdGFza3MnIH0sXG4gICAgICB7IHRpdGxlOiAnRGF0YXNldHMnLCBocmVmOiAnL3dvcmtmbG93cy9kYXRhc2V0cycgfSxcbiAgICAgIHsgdGl0bGU6ICdJdGVyYXRpb24nLCBocmVmOiAnL3dvcmtmbG93cy9pdGVyYXRpb24nIH0sXG4gICAgICB7IHRpdGxlOiAnSW50ZXJuYWwgVG9vbHMnLCBocmVmOiAnL3dvcmtmbG93cy9pbnRlcm5hbC10b29scycgfSxcbiAgICBdLFxuICB9LFxuXTtcblxuZXhwb3J0IGRlZmF1bHQgZnVuY3Rpb24gTGF5b3V0KHsgY2hpbGRyZW4gfSkge1xuICBjb25zdCByb3V0ZXIgPSB1c2VSb3V0ZXIoKTtcblxuICByZXR1cm4gKFxuICAgIDxkaXYgY2xhc3NOYW1lPVwibGF5b3V0XCI+XG4gICAgICA8YXNpZGUgY2xhc3NOYW1lPVwic2lkZWJhclwiPlxuICAgICAgICA8TGluayBocmVmPVwiL1wiIGNsYXNzTmFtZT1cInNpZGViYXItbG9nb1wiPlxuICAgICAgICAgIFNxdWFkXG4gICAgICAgIDwvTGluaz5cbiAgICAgICAgPG5hdj5cbiAgICAgICAgICA8dWw+XG4gICAgICAgICAgICB7bmF2aWdhdGlvbi5tYXAoKHNlY3Rpb24pID0+IChcbiAgICAgICAgICAgICAgPGxpIGtleT17c2VjdGlvbi50aXRsZX0+XG4gICAgICAgICAgICAgICAgPHNwYW4gY2xhc3NOYW1lPVwic2VjdGlvbi10aXRsZVwiPntzZWN0aW9uLnRpdGxlfTwvc3Bhbj5cbiAgICAgICAgICAgICAgICA8dWw+XG4gICAgICAgICAgICAgICAgICB7c2VjdGlvbi5pdGVtcy5tYXAoKGl0ZW0pID0+IChcbiAgICAgICAgICAgICAgICAgICAgPGxpIGtleT17aXRlbS5ocmVmfT5cbiAgICAgICAgICAgICAgICAgICAgICA8TGlua1xuICAgICAgICAgICAgICAgICAgICAgICAgaHJlZj17aXRlbS5ocmVmfVxuICAgICAgICAgICAgICAgICAgICAgICAgY2xhc3NOYW1lPXtyb3V0ZXIucGF0aG5hbWUgPT09IGl0ZW0uaHJlZiA/ICdhY3RpdmUnIDogJyd9XG4gICAgICAgICAgICAgICAgICAgICAgPlxuICAgICAgICAgICAgICAgICAgICAgICAge2l0ZW0udGl0bGV9XG4gICAgICAgICAgICAgICAgICAgICAgPC9MaW5rPlxuICAgICAgICAgICAgICAgICAgICA8L2xpPlxuICAgICAgICAgICAgICAgICAgKSl9XG4gICAgICAgICAgICAgICAgPC91bD5cbiAgICAgICAgICAgICAgPC9saT5cbiAgICAgICAgICAgICkpfVxuICAgICAgICAgIDwvdWw+XG4gICAgICAgIDwvbmF2PlxuICAgICAgPC9hc2lkZT5cbiAgICAgIDxtYWluIGNsYXNzTmFtZT1cIm1haW4tY29udGVudFwiPlxuICAgICAgICA8YXJ0aWNsZSBjbGFzc05hbWU9XCJjb250ZW50XCI+e2NoaWxkcmVufTwvYXJ0aWNsZT5cbiAgICAgIDwvbWFpbj5cbiAgICA8L2Rpdj5cbiAgKTtcbn1cbiJdLCJuYW1lcyI6WyJMaW5rIiwidXNlUm91dGVyIiwibmF2aWdhdGlvbiIsInRpdGxlIiwiaXRlbXMiLCJocmVmIiwiTGF5b3V0IiwiY2hpbGRyZW4iLCJyb3V0ZXIiLCJkaXYiLCJjbGFzc05hbWUiLCJhc2lkZSIsIm5hdiIsInVsIiwibWFwIiwic2VjdGlvbiIsImxpIiwic3BhbiIsIml0ZW0iLCJwYXRobmFtZSIsIm1haW4iLCJhcnRpY2xlIl0sImlnbm9yZUxpc3QiOltdLCJzb3VyY2VSb290IjoiIn0=\n//# sourceURL=webpack-internal:///(pages-dir-node)/./components/Layout.js\n");

/***/ }),

/***/ "(pages-dir-node)/./pages/_app.js":
/*!***********************!*\
  !*** ./pages/_app.js ***!
  \***********************/
/***/ ((__unused_webpack_module, __webpack_exports__, __webpack_require__) => {

"use strict";
eval("__webpack_require__.r(__webpack_exports__);\n/* harmony export */ __webpack_require__.d(__webpack_exports__, {\n/* harmony export */   \"default\": () => (/* binding */ App)\n/* harmony export */ });\n/* harmony import */ var react_jsx_dev_runtime__WEBPACK_IMPORTED_MODULE_0__ = __webpack_require__(/*! react/jsx-dev-runtime */ \"react/jsx-dev-runtime\");\n/* harmony import */ var react_jsx_dev_runtime__WEBPACK_IMPORTED_MODULE_0___default = /*#__PURE__*/__webpack_require__.n(react_jsx_dev_runtime__WEBPACK_IMPORTED_MODULE_0__);\n/* harmony import */ var react__WEBPACK_IMPORTED_MODULE_1__ = __webpack_require__(/*! react */ \"react\");\n/* harmony import */ var react__WEBPACK_IMPORTED_MODULE_1___default = /*#__PURE__*/__webpack_require__.n(react__WEBPACK_IMPORTED_MODULE_1__);\n/* harmony import */ var next_router__WEBPACK_IMPORTED_MODULE_2__ = __webpack_require__(/*! next/router */ \"(pages-dir-node)/./node_modules/next/router.js\");\n/* harmony import */ var next_router__WEBPACK_IMPORTED_MODULE_2___default = /*#__PURE__*/__webpack_require__.n(next_router__WEBPACK_IMPORTED_MODULE_2__);\n/* harmony import */ var _styles_globals_css__WEBPACK_IMPORTED_MODULE_3__ = __webpack_require__(/*! ../styles/globals.css */ \"(pages-dir-node)/./styles/globals.css\");\n/* harmony import */ var _styles_globals_css__WEBPACK_IMPORTED_MODULE_3___default = /*#__PURE__*/__webpack_require__.n(_styles_globals_css__WEBPACK_IMPORTED_MODULE_3__);\n/* harmony import */ var _components_Layout__WEBPACK_IMPORTED_MODULE_4__ = __webpack_require__(/*! ../components/Layout */ \"(pages-dir-node)/./components/Layout.js\");\n\n\n\n\n\nfunction App({ Component, pageProps }) {\n    const router = (0,next_router__WEBPACK_IMPORTED_MODULE_2__.useRouter)();\n    (0,react__WEBPACK_IMPORTED_MODULE_1__.useEffect)({\n        \"App.useEffect\": ()=>{\n            // Highlight code blocks after page loads\n            const highlight = {\n                \"App.useEffect.highlight\": ()=>{\n                    if (false) {}\n                }\n            }[\"App.useEffect.highlight\"];\n            // Initial highlight\n            highlight();\n            // Re-highlight on route changes\n            router.events.on('routeChangeComplete', highlight);\n            return ({\n                \"App.useEffect\": ()=>{\n                    router.events.off('routeChangeComplete', highlight);\n                }\n            })[\"App.useEffect\"];\n        }\n    }[\"App.useEffect\"], [\n        router.events\n    ]);\n    return /*#__PURE__*/ (0,react_jsx_dev_runtime__WEBPACK_IMPORTED_MODULE_0__.jsxDEV)(_components_Layout__WEBPACK_IMPORTED_MODULE_4__[\"default\"], {\n        children: /*#__PURE__*/ (0,react_jsx_dev_runtime__WEBPACK_IMPORTED_MODULE_0__.jsxDEV)(Component, {\n            ...pageProps\n        }, void 0, false, {\n            fileName: \"/Users/maxlund/projects/floze/docs/pages/_app.js\",\n            lineNumber: 29,\n            columnNumber: 7\n        }, this)\n    }, void 0, false, {\n        fileName: \"/Users/maxlund/projects/floze/docs/pages/_app.js\",\n        lineNumber: 28,\n        columnNumber: 5\n    }, this);\n}\n//# sourceURL=[module]\n//# sourceMappingURL=data:application/json;charset=utf-8;base64,eyJ2ZXJzaW9uIjozLCJmaWxlIjoiKHBhZ2VzLWRpci1ub2RlKS8uL3BhZ2VzL19hcHAuanMiLCJtYXBwaW5ncyI6Ijs7Ozs7Ozs7Ozs7Ozs7QUFBa0M7QUFDTTtBQUNUO0FBQ1c7QUFFM0IsU0FBU0csSUFBSSxFQUFFQyxTQUFTLEVBQUVDLFNBQVMsRUFBRTtJQUNsRCxNQUFNQyxTQUFTTCxzREFBU0E7SUFFeEJELGdEQUFTQTt5QkFBQztZQUNSLHlDQUF5QztZQUN6QyxNQUFNTzsyQ0FBWTtvQkFDaEIsSUFBSSxLQUE2QyxFQUFFLEVBRWxEO2dCQUNIOztZQUVBLG9CQUFvQjtZQUNwQkE7WUFFQSxnQ0FBZ0M7WUFDaENELE9BQU9LLE1BQU0sQ0FBQ0MsRUFBRSxDQUFDLHVCQUF1Qkw7WUFDeEM7aUNBQU87b0JBQ0xELE9BQU9LLE1BQU0sQ0FBQ0UsR0FBRyxDQUFDLHVCQUF1Qk47Z0JBQzNDOztRQUNGO3dCQUFHO1FBQUNELE9BQU9LLE1BQU07S0FBQztJQUVsQixxQkFDRSw4REFBQ1QsMERBQU1BO2tCQUNMLDRFQUFDRTtZQUFXLEdBQUdDLFNBQVM7Ozs7Ozs7Ozs7O0FBRzlCIiwic291cmNlcyI6WyIvVXNlcnMvbWF4bHVuZC9wcm9qZWN0cy9mbG96ZS9kb2NzL3BhZ2VzL19hcHAuanMiXSwic291cmNlc0NvbnRlbnQiOlsiaW1wb3J0IHsgdXNlRWZmZWN0IH0gZnJvbSAncmVhY3QnO1xuaW1wb3J0IHsgdXNlUm91dGVyIH0gZnJvbSAnbmV4dC9yb3V0ZXInO1xuaW1wb3J0ICcuLi9zdHlsZXMvZ2xvYmFscy5jc3MnO1xuaW1wb3J0IExheW91dCBmcm9tICcuLi9jb21wb25lbnRzL0xheW91dCc7XG5cbmV4cG9ydCBkZWZhdWx0IGZ1bmN0aW9uIEFwcCh7IENvbXBvbmVudCwgcGFnZVByb3BzIH0pIHtcbiAgY29uc3Qgcm91dGVyID0gdXNlUm91dGVyKCk7XG5cbiAgdXNlRWZmZWN0KCgpID0+IHtcbiAgICAvLyBIaWdobGlnaHQgY29kZSBibG9ja3MgYWZ0ZXIgcGFnZSBsb2Fkc1xuICAgIGNvbnN0IGhpZ2hsaWdodCA9ICgpID0+IHtcbiAgICAgIGlmICh0eXBlb2Ygd2luZG93ICE9PSAndW5kZWZpbmVkJyAmJiB3aW5kb3cuUHJpc20pIHtcbiAgICAgICAgd2luZG93LlByaXNtLmhpZ2hsaWdodEFsbCgpO1xuICAgICAgfVxuICAgIH07XG5cbiAgICAvLyBJbml0aWFsIGhpZ2hsaWdodFxuICAgIGhpZ2hsaWdodCgpO1xuXG4gICAgLy8gUmUtaGlnaGxpZ2h0IG9uIHJvdXRlIGNoYW5nZXNcbiAgICByb3V0ZXIuZXZlbnRzLm9uKCdyb3V0ZUNoYW5nZUNvbXBsZXRlJywgaGlnaGxpZ2h0KTtcbiAgICByZXR1cm4gKCkgPT4ge1xuICAgICAgcm91dGVyLmV2ZW50cy5vZmYoJ3JvdXRlQ2hhbmdlQ29tcGxldGUnLCBoaWdobGlnaHQpO1xuICAgIH07XG4gIH0sIFtyb3V0ZXIuZXZlbnRzXSk7XG5cbiAgcmV0dXJuIChcbiAgICA8TGF5b3V0PlxuICAgICAgPENvbXBvbmVudCB7Li4ucGFnZVByb3BzfSAvPlxuICAgIDwvTGF5b3V0PlxuICApO1xufVxuIl0sIm5hbWVzIjpbInVzZUVmZmVjdCIsInVzZVJvdXRlciIsIkxheW91dCIsIkFwcCIsIkNvbXBvbmVudCIsInBhZ2VQcm9wcyIsInJvdXRlciIsImhpZ2hsaWdodCIsIndpbmRvdyIsIlByaXNtIiwiaGlnaGxpZ2h0QWxsIiwiZXZlbnRzIiwib24iLCJvZmYiXSwiaWdub3JlTGlzdCI6W10sInNvdXJjZVJvb3QiOiIifQ==\n//# sourceURL=webpack-internal:///(pages-dir-node)/./pages/_app.js\n");

/***/ }),

/***/ "(pages-dir-node)/./styles/globals.css":
/*!****************************!*\
  !*** ./styles/globals.css ***!
  \****************************/
/***/ (() => {



/***/ }),

/***/ "fs":
/*!*********************!*\
  !*** external "fs" ***!
  \*********************/
/***/ ((module) => {

"use strict";
module.exports = require("fs");

/***/ }),

/***/ "next/dist/compiled/next-server/pages.runtime.dev.js":
/*!**********************************************************************!*\
  !*** external "next/dist/compiled/next-server/pages.runtime.dev.js" ***!
  \**********************************************************************/
/***/ ((module) => {

"use strict";
module.exports = require("next/dist/compiled/next-server/pages.runtime.dev.js");

/***/ }),

/***/ "react":
/*!************************!*\
  !*** external "react" ***!
  \************************/
/***/ ((module) => {

"use strict";
module.exports = require("react");

/***/ }),

/***/ "react-dom":
/*!****************************!*\
  !*** external "react-dom" ***!
  \****************************/
/***/ ((module) => {

"use strict";
module.exports = require("react-dom");

/***/ }),

/***/ "react/jsx-dev-runtime":
/*!****************************************!*\
  !*** external "react/jsx-dev-runtime" ***!
  \****************************************/
/***/ ((module) => {

"use strict";
module.exports = require("react/jsx-dev-runtime");

/***/ }),

/***/ "react/jsx-runtime":
/*!************************************!*\
  !*** external "react/jsx-runtime" ***!
  \************************************/
/***/ ((module) => {

"use strict";
module.exports = require("react/jsx-runtime");

/***/ }),

/***/ "stream":
/*!*************************!*\
  !*** external "stream" ***!
  \*************************/
/***/ ((module) => {

"use strict";
module.exports = require("stream");

/***/ }),

/***/ "zlib":
/*!***********************!*\
  !*** external "zlib" ***!
  \***********************/
/***/ ((module) => {

"use strict";
module.exports = require("zlib");

/***/ })

};
;

// load runtime
var __webpack_require__ = require("../webpack-runtime.js");
__webpack_require__.C(exports);
var __webpack_exec__ = (moduleId) => (__webpack_require__(__webpack_require__.s = moduleId))
var __webpack_exports__ = __webpack_require__.X(0, ["vendor-chunks/next","vendor-chunks/@swc"], () => (__webpack_exec__("(pages-dir-node)/./pages/_app.js")));
module.exports = __webpack_exports__;

})();